use anyhow::{Result, ensure};
use std::{fs, path::Path};
#[tokio::main]
async fn main() -> Result<()> {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let value = match args
        .iter()
        .map(String::as_str)
        .collect::<Vec<_>>()
        .as_slice()
    {
        [command, mode, directory]
            if matches!(*command, "suite" | "tree")
                && matches!(*mode, "deterministic" | "live") =>
        {
            let connection = if *mode == "live" {
                Some(pot_eval::DeepSeekConnection::from_pi(
                    &std::path::PathBuf::from(std::env::var("HOME")?).join(".pi/agent/models.json"),
                )?)
            } else {
                None
            };
            let report = if *command == "tree" {
                pot_eval::run_delegation_suite(Path::new(directory), connection).await?
            } else {
                pot_eval::run_small_suite(Path::new(directory), connection).await?
            };
            println!("{}", serde_json::to_string_pretty(&report)?);
            ensure!(
                report["success"] == true,
                "Suite failed; records retained in {directory}"
            );
            return Ok(());
        }
        ["run", scenario, directory] => {
            let scenario: pot_eval::Scenario = serde_json::from_slice(&fs::read(scenario)?)?;
            let report = pot_eval::run_scripted(&scenario, Path::new(directory)).await?;
            println!("{}", serde_json::to_string_pretty(&report)?);
            ensure!(
                report.success,
                "Scenario failed; records retained in {directory}"
            );
            return Ok(());
        }
        ["live", scenario, directory] | ["live", scenario, directory, _] => {
            let scenario: pot_eval::Scenario = serde_json::from_slice(&fs::read(scenario)?)?;
            let path = if args.len() == 4 {
                std::path::PathBuf::from(&args[3])
            } else {
                std::path::PathBuf::from(std::env::var("HOME")?).join(".pi/agent/models.json")
            };
            let connection = pot_eval::DeepSeekConnection::from_pi(&path)?;
            let report = pot_eval::run_deepseek(
                &scenario,
                Path::new(directory),
                connection,
                Default::default(),
            )
            .await?;
            println!("{}", serde_json::to_string_pretty(&report)?);
            ensure!(
                report.success,
                "Live scenario failed; records retained in {directory}"
            );
            return Ok(());
        }
        ["validate", directory] => serde_json::to_value(pot_eval::validate(Path::new(directory))?)?,
        ["compare", baseline, candidate] => pot_eval::compare(
            &pot_eval::validate(Path::new(baseline))?,
            &pot_eval::validate(Path::new(candidate))?,
        )?,
        _ => anyhow::bail!(
            "Usage: pot-eval suite|tree deterministic|live NEW_DIR | run SCENARIO.json NEW_RUN_DIR | live SCENARIO.json NEW_RUN_DIR [PI_MODELS.json] | validate RUN_DIR | compare BASELINE_DIR CANDIDATE_DIR"
        ),
    };
    println!("{}", serde_json::to_string_pretty(&value)?);
    Ok(())
}
