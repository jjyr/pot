pub use pot_core::*;
#[cfg(test)]
mod launch_tests {
    use super::*;
    #[test]
    fn agy_native_profile_does_not_claim_unverified_role_capabilities() {
        let mut profile = Profile::legacy(&crate::pi::Config {
            api_key: "unused".into(),
            model: "agy-model".into(),
            max_tokens: 100,
            request_limit: 100,
            max_requests_per_turn: 10,
            max_parallel_tasks: 2,
        });
        profile.engine = Engine::Agy;
        profile.provider = "google".into();
        profile.auth_ref = "cli:agy".into();
        let resolved = profile.resolve().unwrap();
        resolved.validate().unwrap();
        assert_eq!(resolved.launch_spec.as_ref().unwrap().transport, "agy_json");
        assert!(!resolved.capabilities.mcp_tools);
        assert!(!resolved.capabilities.tool_policy_enforcement);
        assert!(!resolved.capabilities.subagents_disableable);
        assert!(!resolved.capabilities.safe_checkpoint_resume);
        crate::agent::routing::check_requirements(
            &resolved,
            &crate::TaskRequirements {
                tools: true,
                ..Default::default()
            },
        )
        .unwrap();
        for requirement in [
            crate::TaskRequirements {
                exact_budget: true,
                ..Default::default()
            },
            crate::TaskRequirements {
                desktop: true,
                ..Default::default()
            },
            crate::TaskRequirements {
                live_steering: true,
                ..Default::default()
            },
        ] {
            assert!(crate::agent::routing::check_requirements(&resolved, &requirement).is_err());
        }
        profile.auth_ref = "cli:claude".into();
        assert!(profile.resolve().is_err());
    }
    #[test]
    fn native_wrapper_arguments_are_preserved_but_managed_flags_are_rejected() {
        for mut launch in [LaunchSpec::codex(), LaunchSpec::claude(), LaunchSpec::agy()] {
            launch.executable = "/opt/my wrapper".into();
            launch.argv = vec![
                LaunchArgument::Literal("--wrapper-label".into()),
                LaunchArgument::Literal("a b;$(no shell)".into()),
                LaunchArgument::RuntimeArguments,
            ];
            assert_eq!(
                launch.expand(&["managed".into()]).unwrap(),
                ["--wrapper-label", "a b;$(no shell)", "managed"]
            );
            for flag in [
                "--config=x",
                "-cfoo=bar",
                "--mcp-config",
                "--allowedTools=Agent",
                "--session-id",
                "--input-format",
                "--effort",
                "--model=x",
            ] {
                launch.argv = vec![
                    LaunchArgument::Literal(flag.into()),
                    LaunchArgument::RuntimeArguments,
                ];
                assert!(launch.validate().is_err(), "accepted {flag}");
            }
        }
    }
    #[test]
    fn command_requires_runtime_policy_and_never_interpolates_shell() {
        let mut launch = LaunchSpec::default();
        let owned = vec!["--session".into(), "/tmp/a b;$(touch unwanted)".into()];
        assert_eq!(launch.expand(&owned).unwrap(), owned);
        launch
            .argv
            .push(LaunchArgument::Literal("--session=other".into()));
        assert!(launch.validate().is_err());
        launch.argv.clear();
        assert!(launch.validate().is_err());
    }
}
