"""Guard component dependency boundaries without building a platform target."""

from pathlib import Path
import tomllib
import unittest

ROOT = Path(__file__).resolve().parents[2]


def dependencies(component):
    manifest = tomllib.loads((ROOT / "crates" / component / "Cargo.toml").read_text())
    return set(manifest.get("dependencies", {}))


class ComponentBoundaries(unittest.TestCase):
    def test_domain_components_do_not_depend_on_composition(self):
        for component in (
            "assistant",
            "gateway",
            "secrets",
            "usage",
            "runner",
            "agent",
            "model-protocol",
        ):
            with self.subTest(component=component):
                self.assertFalse(
                    dependencies(component) & {"pot-control", "pot-runtime", "pot-server"}
                )

    def test_agents_and_runner_do_not_own_provider_credentials(self):
        for component in ("agent", "runner"):
            with self.subTest(component=component):
                self.assertFalse(
                    dependencies(component) & {"pot-gateway", "pot-secrets", "reqwest"}
                )

    def test_model_protocol_has_no_network_or_storage(self):
        self.assertFalse(
            dependencies("model-protocol") & {"reqwest", "rusqlite", "pot-storage", "pot-secrets"}
        )

    def test_runtime_only_composes_components(self):
        self.assertFalse(
            dependencies("runtime") & {"rusqlite", "pot-storage", "pot-secrets", "reqwest", "axum"}
        )


if __name__ == "__main__":
    unittest.main()
