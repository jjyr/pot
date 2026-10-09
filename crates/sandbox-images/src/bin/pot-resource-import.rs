//! Owner/developer offline provisioning. The embedded catalog is still authoritative.
fn main() -> anyhow::Result<()> {
    let args: Vec<_> = std::env::args_os().collect();
    anyhow::ensure!(
        args.len() == 3,
        "usage: pot-resource-import CACHE_ROOT RELEASE_ASSETS_DIR"
    );
    let images = pot_sandbox_images::Images::open(args[1].clone().into())?;
    for r in images.catalog.resources.values() {
        images
            .store
            .import(r, &std::path::PathBuf::from(&args[2]).join(&r.filename))?;
    }
    println!("Verified {} resources", images.catalog.resources.len());
    Ok(())
}
