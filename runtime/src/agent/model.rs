pub use pot_core::*;
#[cfg(test)]
mod launch_tests {
    use super::*;

    #[test]
    fn native_wrapper_arguments_are_preserved_but_managed_flags_are_rejected() {
        for mut launch in [LaunchSpec::codex()] {
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
