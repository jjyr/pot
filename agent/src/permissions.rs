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
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub policy: Option<Policy>,
    pub grants: Vec<ConfiguredGrant>,
}
#[derive(Clone, Copy, Debug, Default, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum DefaultDecision {
    #[default]
    Ask,
    Allow,
}
#[derive(Clone, Copy, Debug, Default, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Policy {
    pub default: DefaultDecision,
}
impl Policy {
    pub fn sandbox() -> Self {
        Self {
            default: DefaultDecision::Allow,
        }
    }
}
impl Default for Config {
    fn default() -> Self {
        Self {
            version: 1,
            policy: None,
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
            policy: None,
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
                    Scope::Path {
                        path,
                        write: matches!(access, Access::ReadWrite),
                        recursive,
                    }
                }
                Permission::Operation { mut tool, mut args } => {
                    // Preserve the old Host command semantics when loading existing grants.
                    if tool == "host_exec_command" {
                        tool = "exec_command".into();
                        let object = args.as_object_mut().ok_or_else(|| {
                            anyhow::anyhow!("Operation arguments must be an object")
                        })?;
                        object.entry("shell").or_insert(Value::from("/bin/sh"));
                        object.entry("login").or_insert(Value::from(false));
                        object.entry("tty").or_insert(Value::from(false));
                        object.entry("timeout_ms").or_insert(Value::from(60000));
                    } else if tool == "pot_manage_sandbox" {
                        tool = "manage_sandbox".into();
                    } else if tool == "host_set_sandbox_mappings" {
                        tool = "set_sandbox_mappings".into();
                    }
                    ensure!(
                        is_execution_tool(&tool)
                            || tool == "manage_sandbox"
                            || tool == "set_sandbox_mappings",
                        "Unsupported permission tool"
                    );
                    ensure!(args.is_object(), "Operation arguments must be an object");
                    if tool == "exec_command" {
                        let defaults = pot_core::ShellSettings::default();
                        let object = args.as_object_mut().unwrap();
                        object
                            .entry("shell")
                            .or_insert(Value::from(defaults.executable));
                        object
                            .entry("login")
                            .or_insert(Value::from(defaults.allow_login_shell));
                        object.entry("tty").or_insert(Value::from(false));
                        if !object.contains_key("yield_time_ms") {
                            object
                                .entry("timeout_ms")
                                .or_insert(Value::from(defaults.default_timeout_ms));
                        }
                    }
                    // Config paths refer to their own environment, never the Runtime filesystem.
                    if is_execution_tool(&tool) {
                        pot_core::environment_tools::validate(&tool, &args)?;
                    }
                    if let Some(object) = args.as_object_mut() {
                        object.remove("environment");
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
    #[serde(default = "default_environment")]
    pub environment: String,
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
    Ok(Scope::Exact {
        tool: tool.into(),
        args: {
            let mut value = args.clone();
            if let Some(object) = value.as_object_mut() {
                object.remove("environment");
            }
            value
        },
    })
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

fn default_environment() -> String {
    "host".into()
}
pub fn is_execution_tool(name: &str) -> bool {
    ["exec_command", "write_stdin", "apply_patch"].contains(&name)
}

pub async fn authorize(
    context: &ExecutionContext,
    binding: &Binding,
    turn: &TurnRequest,
    call_id: &str,
    tool: &str,
    args: &Value,
) -> Result<()> {
    if context.role == ExecutionRole::Summary {
        ensure!(
            !is_execution_tool(tool) && !["manage_sandbox", "set_sandbox_mappings"].contains(&tool),
            "Summary is read-only"
        );
    }
    if tool == "manage_sandbox" {
        ensure!(
            context.role == ExecutionRole::Main,
            "Only Main can manage Sandboxes"
        );
    }
    let environment = args["environment"].as_str().unwrap_or("host");
    if tool == "set_sandbox_mappings" {
        ensure!(
            context.role == ExecutionRole::Main && environment == "host",
            "Only Main on Host can modify Sandbox mappings"
        );
    }
    // Product management is trusted role-scoped capability. Session input/polls
    // continue an existing authorized command; the executor checks ownership.
    if tool == "write_stdin" || (!is_execution_tool(tool) && tool != "set_sandbox_mappings") {
        return Ok(());
    }
    let Some(state) = context.feedback.environment_grants(environment).await? else {
        return Ok(());
    };
    ensure!(
        !context.feedback.cancelled(),
        "Permission request cancelled"
    );
    // Legacy embedder feedback supplies a grant array and retains ask-by-default.
    let (policy, grants) = if state.is_array() {
        (Policy::default(), state)
    } else {
        (
            serde_json::from_value::<Policy>(state["policy"].clone())?,
            state["grants"].clone(),
        )
    };
    if policy.default == DefaultDecision::Allow && tool != "set_sandbox_mappings" {
        return Ok(());
    }
    let request = Request {
        owner: context.owner.clone(),
        environment: environment.into(),
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
    fn legacy_host_commands_keep_their_shell_and_guest_paths_stay_lexical() {
        let config = serde_json::json!({"version":1,"grants":[{"id":"old","agent":"main","permission":{"type":"operation","tool":"host_exec_command","args":{"cmd":"true","workdir":"/guest-only/nonexistent"}}}]});
        let grants = serde_json::from_value::<Config>(config)
            .unwrap()
            .into_grants()
            .unwrap();
        assert_eq!(
            grants[0].scope,
            Scope::Exact {
                tool: "exec_command".into(),
                args: serde_json::json!({"cmd":"true","workdir":"/guest-only/nonexistent","shell":"/bin/sh","login":false,"tty":false,"timeout_ms":60000})
            }
        );
        let args =
            serde_json::json!({"path":"/guest-only/nonexistent","environment":"sandbox:one"});
        assert_eq!(
            scope("apply_patch", &args).unwrap(),
            Scope::Exact {
                tool: "apply_patch".into(),
                args: serde_json::json!({"path":"/guest-only/nonexistent"})
            }
        );
    }
    #[test]
    fn editable_config_roundtrips_and_rejects_ambiguous_permissions() {
        let valid = serde_json::json!({"version":1,"grants":[
            {"id":"notes","agent":"main","permission":{"type":"path","path":"/","access":"read","recursive":true}},
            {"id":"command","agent":"task-123","permission":{"type":"operation","tool":"exec_command","args":{"cmd":"true","workdir":"/"}}}
        ]});
        let grants = serde_json::from_value::<Config>(valid.clone())
            .unwrap()
            .into_grants()
            .unwrap();
        assert_eq!(
            grants[1].scope,
            Scope::Exact {
                tool: "exec_command".into(),
                args: serde_json::json!({"cmd":"true","workdir":"/","shell":"/bin/bash","login":true,"tty":false,"timeout_ms":120000})
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
            tool: "exec_command".into(),
            args: serde_json::json!({"cmd":"cat /tmp/project/file"})
        }));
        let exact = Scope::Exact {
            tool: "exec_command".into(),
            args: serde_json::json!({"cmd":"true"}),
        };
        assert!(!exact.covers(&Scope::Exact {
            tool: "exec_command".into(),
            args: serde_json::json!({"cmd":"false"})
        }));
    }
}
