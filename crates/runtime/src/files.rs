//! Scoped, bounded file reads. Downloads never execute a file or invoke Agent tools.
use crate::*;
use axum::{
    body::Body,
    extract::{Path, Query, State},
    response::Response,
};
use base64::{Engine, engine::general_purpose::STANDARD};
use pot_protocol::{FileChunk, FileQuery};
use std::{
    fs::File,
    io::{Read, Seek, SeekFrom},
    os::unix::{
        fs::{MetadataExt, OpenOptionsExt},
        io::{AsRawFd, FromRawFd},
    },
};

pub const CHUNK_BYTES: usize = 192 * 1024;
pub const MAX_FILE_BYTES: u64 = 1024 * 1024 * 1024;

pub fn read_chunk(root: &std::path::Path, query: &FileQuery) -> Result<FileChunk> {
    anyhow::ensure!(
        pot_protocol::valid_file_path(&query.path),
        "Invalid relative file path"
    );
    let mut file = std::fs::OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_DIRECTORY | libc::O_NOFOLLOW)
        .open(root)?;
    let parts: Vec<_> = query.path.split('/').collect();
    for (n, part) in parts.iter().enumerate() {
        let name = std::ffi::CString::new(*part)?;
        let flags = libc::O_RDONLY
            | libc::O_CLOEXEC
            | libc::O_NOFOLLOW
            | libc::O_NONBLOCK
            | if n + 1 < parts.len() {
                libc::O_DIRECTORY
            } else {
                0
            };
        let fd = unsafe { libc::openat(file.as_raw_fd(), name.as_ptr(), flags) };
        if fd < 0 {
            return Err(std::io::Error::last_os_error().into());
        }
        file = unsafe { File::from_raw_fd(fd) };
    }
    let metadata = file.metadata()?;
    anyhow::ensure!(
        metadata.is_file() && metadata.len() <= MAX_FILE_BYTES,
        "File is unavailable or exceeds 1 GiB"
    );
    let revision = format!(
        "{}-{}-{}-{}-{}-{}-{}",
        metadata.dev(),
        metadata.ino(),
        metadata.len(),
        metadata.mtime(),
        metadata.mtime_nsec(),
        metadata.ctime(),
        metadata.ctime_nsec()
    );
    anyhow::ensure!(
        query.revision.is_empty() || query.revision == revision,
        "File changed; start a new download"
    );
    anyhow::ensure!(query.offset <= metadata.len(), "Invalid file offset");
    file.seek(SeekFrom::Start(query.offset))?;
    let mut bytes = vec![0; (metadata.len() - query.offset).min(CHUNK_BYTES as u64) as usize];
    file.read_exact(&mut bytes)?;
    let after = file.metadata()?;
    anyhow::ensure!(
        metadata.len() == after.len()
            && metadata.mtime() == after.mtime()
            && metadata.mtime_nsec() == after.mtime_nsec()
            && metadata.ctime() == after.ctime()
            && metadata.ctime_nsec() == after.ctime_nsec(),
        "File changed during download"
    );
    Ok(FileChunk {
        size: metadata.len(),
        offset: query.offset,
        revision,
        data: STANDARD.encode(bytes),
    })
}

async fn task_chunk(root: &Service, task_id: &str, query: &FileQuery) -> Result<FileChunk> {
    let task = tasks::control_service(root, task_id).map_err(|e| anyhow!(e.1.0.message))?;
    let environment = task
        .environment_ref
        .as_ref()
        .ok_or_else(|| anyhow!("Task has no environment"))?;
    if environment.is_local_to(&root.runtime_id) {
        let workspace = task.workspace.clone();
        let query = query.clone();
        tokio::task::spawn_blocking(move || read_chunk(&workspace, &query)).await?
    } else {
        crate::remote_environment::port(root)?
            .read_file(environment.clone(), task_id.into(), query.clone())
            .await
    }
}
fn failure(e: anyhow::Error) -> (StatusCode, Json<ApiError>) {
    error(
        StatusCode::BAD_REQUEST,
        "file_unavailable",
        &format!("File unavailable: {e}"),
    )
}
pub async fn chunk(
    State(root): State<Service>,
    h: HeaderMap,
    Path(task): Path<String>,
    Query(query): Query<FileQuery>,
) -> ApiResult<Json<FileChunk>> {
    root.authorized(&h)?;
    if root.is_worker {
        return Err(error(
            StatusCode::FORBIDDEN,
            "owner_required",
            "Owner required",
        ));
    }
    task_chunk(&root, &task, &query)
        .await
        .map(Json)
        .map_err(failure)
}
pub async fn download(
    State(root): State<Service>,
    h: HeaderMap,
    Path(task): Path<String>,
    Query(mut query): Query<FileQuery>,
) -> ApiResult<Response> {
    root.authorized(&h)?;
    if root.is_worker {
        return Err(error(
            StatusCode::FORBIDDEN,
            "owner_required",
            "Owner required",
        ));
    }
    query.offset = 0;
    let first = task_chunk(&root, &task, &query).await.map_err(failure)?;
    let initial_bytes = STANDARD
        .decode(&first.data)
        .map_err(|e| failure(e.into()))?;
    if first.size > MAX_FILE_BYTES
        || first.offset != 0
        || first.revision.is_empty()
        || initial_bytes.len() > CHUNK_BYTES
        || initial_bytes.len() as u64 > first.size
        || (initial_bytes.is_empty() && first.size != 0)
    {
        return Err(failure(anyhow!("Invalid file chunk")));
    }
    let size = first.size;
    query.revision = first.revision.clone();
    let filename = query.path.rsplit('/').next().unwrap();
    let filename =
        percent_encoding::utf8_percent_encode(filename, percent_encoding::NON_ALPHANUMERIC)
            .to_string();
    let stream = async_stream::try_stream! {
        let mut chunk = first;
        loop {
            let bytes = STANDARD.decode(&chunk.data)?;
            let end = chunk.offset.checked_add(bytes.len() as u64)
                .ok_or_else(|| anyhow!("Invalid file offset"))?;
            if !(chunk.size == size && chunk.revision == query.revision && chunk.offset == query.offset
                && bytes.len() <= CHUNK_BYTES && end <= size && (end == size || !bytes.is_empty())) {
                Err(anyhow!("Invalid file chunk"))?;
            }
            yield axum::body::Bytes::from(bytes);
            if end == size { break; }
            query.offset = end;
            chunk = task_chunk(&root, &task, &query).await?;
        }
    };
    let stream: std::pin::Pin<Box<dyn futures::Stream<Item = Result<axum::body::Bytes>> + Send>> =
        Box::pin(stream);
    Ok(Response::builder()
        .header("content-type", "application/octet-stream")
        .header(
            "content-disposition",
            format!("attachment; filename*=UTF-8''{filename}"),
        )
        .header("content-length", size.to_string())
        .header("cache-control", "no-store")
        .header("x-content-type-options", "nosniff")
        .body(Body::from_stream(stream))
        .unwrap())
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn chunks_preserve_binary_content_and_reject_changes_and_escapes() {
        let root = std::env::temp_dir().join(format!("pot-files-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir_all(root.join("nested")).unwrap();
        let bytes: Vec<_> = (0..CHUNK_BYTES * 2 + 23).map(|n| n as u8).collect();
        std::fs::write(root.join("nested/report.bin"), &bytes).unwrap();
        let mut query = FileQuery {
            path: "nested/report.bin".into(),
            ..Default::default()
        };
        let mut received = Vec::new();
        loop {
            let chunk = read_chunk(&root, &query).unwrap();
            query.revision = chunk.revision;
            let decoded = STANDARD.decode(chunk.data).unwrap();
            assert!(decoded.len() <= CHUNK_BYTES);
            query.offset += decoded.len() as u64;
            received.extend(decoded);
            if query.offset == chunk.size {
                break;
            }
        }
        assert_eq!(received, bytes);
        std::fs::write(root.join("nested/report.bin"), "changed").unwrap();
        query.offset = 0;
        assert!(read_chunk(&root, &query).is_err());
        std::os::unix::fs::symlink("/etc/passwd", root.join("link")).unwrap();
        std::os::unix::fs::symlink("/etc", root.join("directory-link")).unwrap();
        for path in [
            "../x",
            "/etc/passwd",
            "link",
            "directory-link/passwd",
            "nested",
            "missing",
        ] {
            assert!(
                read_chunk(
                    &root,
                    &FileQuery {
                        path: path.into(),
                        ..Default::default()
                    }
                )
                .is_err()
            );
        }
        std::fs::remove_dir_all(root).unwrap();
    }
}
