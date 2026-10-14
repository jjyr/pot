//! Optional listener TLS. Deployment chooses encryption independently of business APIs.
use anyhow::{Result, ensure};
use std::path::Path;

pub(crate) async fn config(
    certificate: &Path,
    private_key: &Path,
) -> Result<Option<axum_server::tls_rustls::RustlsConfig>> {
    let certificate_set = !certificate.as_os_str().is_empty();
    let key_set = !private_key.as_os_str().is_empty();
    ensure!(
        certificate_set == key_set,
        "Configure both TLS certificate and private key, or neither"
    );
    if !certificate_set {
        return Ok(None);
    }
    ensure!(
        certificate.is_absolute() && private_key.is_absolute(),
        "TLS files must use absolute paths"
    );
    let _ = rustls::crypto::ring::default_provider().install_default();
    Ok(Some(
        axum_server::tls_rustls::RustlsConfig::from_pem_file(certificate, private_key).await?,
    ))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn incomplete_or_relative_tls_configuration_is_rejected() {
        assert!(
            config(Path::new(""), Path::new(""))
                .await
                .unwrap()
                .is_none()
        );
        for (certificate, key) in [
            ("/tmp/cert.pem", ""),
            ("", "/tmp/key.pem"),
            ("cert.pem", "/tmp/key.pem"),
            ("/tmp/cert.pem", "key.pem"),
        ] {
            assert!(
                config(Path::new(certificate), Path::new(key))
                    .await
                    .is_err()
            );
        }
    }
}
