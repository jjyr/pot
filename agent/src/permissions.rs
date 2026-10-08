//! Agent-owned permission policy. Embedders supply durable grants and owner UI,
//! never model-generated approval. Drivers other than pot-agent do not use this.
use anyhow::{Result, ensure};
use pot_core::{Binding, ExecutionContext, ExecutionRole, TurnRequest};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::path::{Component, Path, PathBuf};

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
pub enum Scope {
    Exact {
        tool: String,
        args: Value,
    },
    Path {
        path: PathBuf,
        write: bool,
        recursive: bool,
    },
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Grant {
    pub id: String,
    pub owner: String,
    pub scope: Scope,
}

/// User-editable current permissions, independent of execution bindings/history.
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Config {
    pub version: u32,
    pub grants: Vec<ConfiguredGrant>,
}
impl Default for Config {
    fn default() -> Self {
        Self {
            version: 1,
            grants: vec![],
        }
    }
}
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ConfiguredGrant {
    pub id: String,
    pub agent: String,
    pub permission: Permission,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "snake_case", deny_unknown_fields)]
pub enum Permission {
    Path {
        path: PathBuf,
        access: Access,
        recursive: bool,
    },
    Operation {
        tool: String,
        args: Value,
    },
}
#[derive(Clone, Copy, Debug, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Access {
    Read,
    ReadWrite,
}
impl Config {
    pub fn from_grants(grants: &[Grant]) -> Self {
        Self {
            version: 1,
            grants: grants
                .iter()
                .map(|g| ConfiguredGrant {
                    id: g.id.clone(),
                    agent: g.owner.clone(),
                    permission: match &g.scope {
                        Scope::Path {
                            path,
                            write,
                            recursive,
                        } => Permission::Path {
                            path: path.clone(),
                            access: if *write {
                                Access::ReadWrite
                            } else {
                                Access::Read
                            },
                            recursive: *recursive,
                        },
                        Scope::Exact { tool, args } => Permission::Operation {
                            tool: tool.clone(),
                            args: args.clone(),
                        },
                    },
                })
                .collect(),
        }
    }
    pub fn into_grants(self) -> Result<Vec<Grant>> {
        ensure!(self.version == 1, "Unsupported permissions.json version");
        ensure!(self.grants.len() <= 1000, "Persistent grant limit reached");
        let mut grants: Vec<Grant> = vec![];
        for g in self.grants {
            ensure!(
                !g.id.trim().is_empty() && !g.agent.trim().is_empty() && !g.agent.contains('*'),
                "Grant ID and explicit Agent identity required"
            );
            let scope = match g.permission {
                Permission::Path {
                    path,
                    access,
                    recursive,
                } => {
                    ensure!(
                        path.is_absolute()
                            && !path.components().any(|c| matches!(c, Component::ParentDir)),
                        "Grant path must be absolute without parent components"
                    );
                    // Preserve absent paths, but never silently expand an existing symlink grant.
                    if path.exists() {
                        ensure!(path.canonicalize()? == path, "Grant path must be canonical");
                    }
                    Scope::Path {
                        path,
                        write: matches!(access, Access::ReadWrite),
                        recursive,
                    }
                }
                Permission::Operation { tool, mut args } => {
                    ensure!(
                        tool == "pot_manage_sandbox" || tool.starts_with("host_"),
                        "Unsupported permission tool"
                    );
                    ensure!(args.is_object(), "Operation arguments must be an object");
                    if tool.starts_with("host_") {
                        crate::host_tools::normalize(&tool, &mut args)?;
                    }
                    Scope::Exact { tool, args }
                }
            };
            ensure!(
                !grants
                    .iter()
                    .any(|old| old.id == g.id || (old.owner == g.agent && old.scope == scope)),
                "Duplicate grant ID or Agent permission scope"
            );
            grants.push(Grant {
                id: g.id,
                owner: g.agent,
                scope,
            });
        }
        Ok(grants)
    }
}
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Request {
    pub owner: String,
    pub epoch: u64,
    pub turn: String,
    pub call_id: String,
    pub tool: String,
    pub args: Value,
    pub suggested_scope: Scope,
}

/// Resolve before review, then execute this exact path without following links.
/// New files resolve through their existing parent. No filesystem content is read.
pub fn resolve_path(path: &str) -> Result<PathBuf> {
    let path = Path::new(path);
    ensure!(path.is_absolute(), "Host paths must be absolute");
    ensure!(
        !path.components().any(|c| matches!(c, Component::ParentDir)),
        "Parent path components are not allowed"
    );
    if path.exists() {
        Ok(path.canonicalize()?)
    } else {
        let parent = path
            .parent()
            .ok_or_else(|| anyhow::anyhow!("Missing parent directory"))?
            .canonicalize()?;
        Ok(parent.join(
            path.file_name()
                .ok_or_else(|| anyhow::anyhow!("Missing filename"))?,
        ))
    }
}

pub fn scope(tool: &str, args: &Value) -> Result<Scope> {
    if matches!(
        tool,
        "host_read_file" | "host_write_file" | "host_list_directory"
    ) {
        let path = resolve_path(
            args["path"]
                .as_str()
                .ok_or_else(|| anyhow::anyhow!("Host path required"))?,
        )?;
        Ok(Scope::Path {
            recursive: tool == "host_list_directory",
            path,
            write: tool == "host_write_file",
        })
    } else {
        Ok(Scope::Exact {
            tool: tool.into(),
            args: args.clone(),
        })
    }
}
impl Scope {
    pub fn covers(&self, requested: &Scope) -> bool {
        match (self, requested) {
            (
                Self::Exact { tool, args },
                Self::Exact {
                    tool: other,
                    args: values,
                },
            ) => tool == other && args == values,
            (
                Self::Path {
                    path,
                    write,
                    recursive,
                },
                Self::Path {
                    path: other,
                    write: writes,
                    ..
                },
            ) => (*write || !writes) && (path == other || (*recursive && other.starts_with(path))),
            _ => false,
        }
    }
}

pub async fn authorize(
    context: &ExecutionContext,
    binding: &Binding,
    turn: &TurnRequest,
    call_id: &str,
    tool: &str,
    args: &Value,
) -> Result<()> {
    let Some(grants) = context.feedback.permission_grants()? else {
        ensure!(
            !tool.starts_with("host_"),
            "Host permission service unavailable"
        );
        return Ok(());
    };
    ensure!(
        !context.feedback.cancelled(),
        "Permission request cancelled"
    );
    if context.role == ExecutionRole::Summary {
        ensure!(
            matches!(tool, "pot_list_tasks" | "pot_get_task"),
            "Summary is read-only"
        );
        return Ok(());
    }
    // Ordinary Sandbox execution and explicitly exposed product/extension tools
    // retain their current policy. Host tools and environment mutations ask.
    if !tool.starts_with("host_") && tool != "pot_manage_sandbox" {
        return Ok(());
    }
    if tool.starts_with("host_") {
        if tool == "host_set_sandbox_mappings" {
            ensure!(
                context.role == ExecutionRole::Main,
                "Only Main can modify Sandbox mappings"
            );
        }
        crate::host_tools::validate(tool, args)?;
    }
    let request = Request {
        owner: context.owner.clone(),
        epoch: binding.epoch,
        turn: turn.request_id.clone(),
        call_id: call_id.into(),
        tool: tool.into(),
        args: args.clone(),
        suggested_scope: scope(tool, args)?,
    };
    let grants: Vec<Grant> = serde_json::from_value(grants)?;
    if grants
        .iter()
        .any(|g| g.owner == request.owner && g.scope.covers(&request.suggested_scope))
    {
        return Ok(());
    }
    let decision = tokio::select! {
        result = context.feedback.request_permission(serde_json::to_value(&request)?) => result?,
        _ = context.feedback.cancellation() => anyhow::bail!("Permission request cancelled"),
    };
    ensure!(
        !context.feedback.cancelled(),
        "Permission request cancelled"
    );
    ensure!(
        decision["request"] == serde_json::to_value(&request)? && decision["approved"] == true,
        "Owner denied permission"
    );
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn editable_config_roundtrips_and_rejects_ambiguous_permissions() {
        let valid = serde_json::json!({"version":1,"grants":[
            {"id":"notes","agent":"main","permission":{"type":"path","path":"/","access":"read","recursive":true}},
            {"id":"command","agent":"task-123","permission":{"type":"operation","tool":"host_exec_command","args":{"cmd":"true","workdir":"/"}}}
        ]});
        let grants = serde_json::from_value::<Config>(valid.clone())
            .unwrap()
            .into_grants()
            .unwrap();
        assert_eq!(
            grants[1].scope,
            Scope::Exact {
                tool: "host_exec_command".into(),
                args: serde_json::json!({"cmd":"true","workdir":"/","timeout_ms":60000})
            }
        );
        assert_eq!(Config::from_grants(&grants).into_grants().unwrap(), grants);
        for change in [
            serde_json::json!({"version":2,"grants":[]}),
            serde_json::json!({"version":1,"grants":[],"requests":[]}),
        ] {
            assert!(
                serde_json::from_value::<Config>(change)
                    .and_then(|c| c.into_grants().map_err(serde::de::Error::custom))
                    .is_err()
            );
        }
        for (field, value) in [
            ("agent", serde_json::json!("*")),
            ("id", serde_json::json!("")),
        ] {
            let mut invalid = valid.clone();
            invalid["grants"][0][field] = value;
            assert!(
                serde_json::from_value::<Config>(invalid)
                    .unwrap()
                    .into_grants()
                    .is_err()
            );
        }
        let mut duplicate = valid.clone();
        duplicate["grants"]
            .as_array_mut()
            .unwrap()
            .push(valid["grants"][0].clone());
        assert!(
            serde_json::from_value::<Config>(duplicate)
                .unwrap()
                .into_grants()
                .is_err()
        );
        let mut traversal = valid;
        traversal["grants"][0]["permission"]["path"] = serde_json::json!("/tmp/../secret");
        assert!(
            serde_json::from_value::<Config>(traversal)
                .unwrap()
                .into_grants()
                .is_err()
        );
    }
    #[test]
    fn path_grants_do_not_cover_siblings_shell_or_writes_without_write_access() {
        let grant = Scope::Path {
            path: "/tmp/project".into(),
            write: false,
            recursive: true,
        };
        assert!(grant.covers(&Scope::Path {
            path: "/tmp/project/sub/file".into(),
            write: false,
            recursive: false
        }));
        assert!(!grant.covers(&Scope::Path {
            path: "/tmp/project-two/file".into(),
            write: false,
            recursive: false
        }));
        assert!(!grant.covers(&Scope::Path {
            path: "/tmp/project/file".into(),
            write: true,
            recursive: false
        }));
        assert!(!grant.covers(&Scope::Exact {
            tool: "host_exec_command".into(),
            args: serde_json::json!({"cmd":"cat /tmp/project/file"})
        }));
        let exact = Scope::Exact {
            tool: "host_exec_command".into(),
            args: serde_json::json!({"cmd":"true"}),
        };
        assert!(!exact.covers(&Scope::Exact {
            tool: "host_exec_command".into(),
            args: serde_json::json!({"cmd":"false"})
        }));
    }
}
