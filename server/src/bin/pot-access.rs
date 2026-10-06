//! Trusted local/SSH administration; never print the private Runtime bearer.
use anyhow::{Result, bail};
use pot_protocol::RuntimeEndpoint;
use serde_json::{Value, json};
#[tokio::main]
async fn main() -> Result<()> {
    let args = std::env::args().skip(1).collect::<Vec<_>>();
    let action = args.first().map(String::as_str).unwrap_or("status");
    let data = std::env::var("POT_DATA_DIR").unwrap_or_else(|_| "product".into());
    let endpoint: RuntimeEndpoint = serde_json::from_slice(&std::fs::read(
        std::path::Path::new(&data).join("runtime.json"),
    )?)?;
    let url = format!("http://127.0.0.1:{}/api/v1/access", endpoint.port);
    let client = reqwest::Client::new();
    let response = if matches!(action, "status" | "qr") {
        client.get(&url).bearer_auth(&endpoint.token).send().await?
    } else {
        let command = match action {
            "pair" | "close_enrollment" | "enable" | "disable" => json!({"action":action}),
            "approve" | "revoke_session" | "revoke_credential" | "revoke_subscription" => {
                json!({"action":action,"id":args.get(1).ok_or_else(||anyhow::anyhow!("Request/credential/session ID required"))?})
            }
            _ => bail!(
                "Commands: qr, status, pair, approve ID, close_enrollment, revoke_session ID, revoke_credential ID, revoke_subscription ID, enable, disable"
            ),
        };
        client
            .post(&url)
            .bearer_auth(&endpoint.token)
            .json(&command)
            .send()
            .await?
    };
    if !response.status().is_success() {
        bail!("Access administration failed: {}", response.status());
    }
    let value: Value = response.json().await?;
    if action == "qr" {
        let origin = value["origin"]
            .as_str()
            .ok_or_else(|| anyhow::anyhow!("Missing origin"))?;
        let code = qrcode::QrCode::new(origin)?;
        println!(
            "{}\n{}",
            origin,
            code.render::<qrcode::render::unicode::Dense1x2>()
                .quiet_zone(true)
                .build()
        );
    } else {
        println!("{}", serde_json::to_string_pretty(&value)?);
    }
    Ok(())
}
