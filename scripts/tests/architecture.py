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
            "runtime-execution",
            "agent",
            "model-protocol",
        ):
            with self.subTest(component=component):
                self.assertFalse(
                    dependencies(component) & {"pot-control", "pot-runtime", "pot-server"}
                )

    def test_agents_and_runner_do_not_own_provider_credentials(self):
        for component in ("agent", "runner", "runtime-execution"):
            with self.subTest(component=component):
                self.assertFalse(
                    dependencies(component) & {"pot-gateway", "pot-secrets", "reqwest"}
                )

    def test_model_protocol_has_no_network_or_storage(self):
        self.assertFalse(
            dependencies("model-protocol") & {"reqwest", "rusqlite", "pot-storage", "pot-secrets"}
        )

    def test_runtime_does_not_own_business_storage_or_transport(self):
        self.assertFalse(
            dependencies("runtime") & {"rusqlite", "pot-storage", "pot-secrets", "reqwest", "axum"}
        )

    def test_product_agent_invocation_belongs_to_runtime_execution(self):
        # Isolated engine/evaluation fixtures may invoke the engine directly.
        # Product adapters may only submit to their Runtime's shared manager.
        for component in ("control", "runner", "runtime", "server", "assistant"):
            for source in (ROOT / "crates" / component / "src").rglob("*.rs"):
                with self.subTest(source=source):
                    self.assertNotIn("pot_agent::Agent::", source.read_text())
        for component in ("control", "runner", "runtime"):
            self.assertIn("pot-runtime-execution", dependencies(component))


if __name__ == "__main__":
    unittest.main()
