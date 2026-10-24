#!/usr/bin/env python3
"""Guard that future concurrency tests cannot silently leave the race suite."""

import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("test_go", Path(__file__).parents[1] / "test_go.py")
PLAN = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PLAN)


class RacePlanTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name).resolve()
        patcher = patch.object(PLAN, "ROOT", self.root)
        patcher.start()
        self.addCleanup(patcher.stop)

    def package(self, name, tests):
        directory = self.root / name
        directory.mkdir(parents=True)
        (directory / "case_test.go").write_text(
            'package fixture\nimport "testing"\n'
            + "\n".join(f"func {test}(t *testing.T) {{}}" for test in tests)
        )
        return {"Dir": str(directory), "TestGoFiles": ["case_test.go"]}

    def test_new_test_in_excluded_file_gets_race(self):
        package = self.package("internal/control", ["TestConfig", "TestNewConcurrency"])
        full, selected = PLAN.race_plan([package], {"./internal/control": {"TestConfig"}})
        self.assertEqual(full, [])
        self.assertEqual(selected, [("./internal/control", ["TestNewConcurrency"])])

    def test_new_package_gets_full_race(self):
        package = self.package("pkg/new", ["TestConcurrent"])
        self.assertEqual(PLAN.race_plan([package], {}), (["./pkg/new"], []))

    def test_race_only_files_enter_the_actual_plan(self):
        (self.root / "go.mod").write_text("module example.com/race-plan\n\ngo 1.27\n")
        package = self.package("pkg/mixed", ["TestSerial", "TestConcurrent"])
        directory = Path(package["Dir"])
        (directory / "race_test.go").write_text(
            '//go:build race\n\npackage fixture\nimport "testing"\n'
            "func TestRaceOnly(t *testing.T) {}\n"
        )
        only = self.root / "pkg/raceonly"
        only.mkdir()
        (only / "fixture.go").write_text("package fixture\n")
        (only / "race_test.go").write_text(
            '//go:build race\n\npackage fixture\nimport "testing"\n'
            "func TestRaceOnly(t *testing.T) {}\n"
        )
        manifest = self.root / "normal-only.json"
        manifest.write_text(
            json.dumps(
                {
                    "normal_only": [
                        {
                            "package": "./pkg/mixed",
                            "reason": "Serial contract",
                            "tests": ["TestSerial"],
                        }
                    ]
                }
            )
        )
        with (
            patch.object(PLAN, "MANIFEST", manifest),
            patch.object(PLAN.sys, "argv", ["test_go.py", "race"]),
            patch.object(PLAN, "run", return_value=0) as run,
        ):
            with self.assertRaises(SystemExit) as result:
                PLAN.main()
        self.assertEqual(result.exception.code, 0)
        commands = [call.args[0] for call in run.call_args_list]
        self.assertTrue(any("./pkg/raceonly" in command for command in commands))
        mixed = next(command for command in commands if "./pkg/mixed" in command)
        self.assertEqual(mixed[mixed.index("-run") + 1], "^(TestConcurrent|TestRaceOnly)$")

    def test_entirely_serial_package_skips_only_named_tests(self):
        package = self.package("pkg/values", ["TestValue"])
        self.assertEqual(PLAN.race_plan([package], {"./pkg/values": {"TestValue"}}), ([], []))
        self.assertEqual(
            PLAN.race_plan([package], {"./pkg/values": {"TestRemoved"}}), (["./pkg/values"], [])
        )

    def test_stale_and_duplicate_exclusions_fail(self):
        self.package("pkg/values", ["TestValue"])
        group = {"package": "./pkg/values", "reason": "Serial contract", "tests": ["TestValue"]}
        self.assertEqual(
            PLAN.normal_only({"normal_only": [group]}), {"./pkg/values": {"TestValue"}}
        )
        with self.assertRaisesRegex(ValueError, "duplicate"):
            PLAN.normal_only({"normal_only": [group, group]})
        with self.assertRaisesRegex(ValueError, "stale"):
            PLAN.normal_only({"normal_only": [{**group, "tests": ["TestRemoved"]}]})

    def test_go_list_stream_and_platform_files(self):
        package = self.package("pkg/platform", ["TestActive"])
        (Path(package["Dir"]) / "case_darwin_test.go").write_text(
            "func TestOtherOS(t *testing.T) {}"
        )
        self.assertEqual(PLAN.race_plan([package], {}), (["./pkg/platform"], []))
        self.assertEqual(
            PLAN.decode_packages('{"Dir":"a"}\n{"Dir":"b"}\n'), [{"Dir": "a"}, {"Dir": "b"}]
        )

    def test_failure_in_either_group_fails_the_check(self):
        packages = [
            self.package("internal/control", ["TestConfig", "TestConcurrent"]),
            self.package("pkg/other", ["TestConcurrent"]),
        ]
        manifest = self.root / "normal-only.json"
        manifest.write_text(
            json.dumps(
                {
                    "normal_only": [
                        {
                            "package": "./internal/control",
                            "reason": "Serial configuration",
                            "tests": ["TestConfig"],
                        }
                    ]
                }
            )
        )
        output = "\n".join(json.dumps(package) for package in packages)
        for failed in ["./internal/control", "./pkg/other"]:
            with (
                self.subTest(failed=failed),
                patch.object(PLAN, "MANIFEST", manifest),
                patch.object(PLAN.sys, "argv", ["test_go.py", "race"]),
                patch.object(PLAN.subprocess, "check_output", return_value=output),
                patch.object(
                    PLAN, "run", side_effect=lambda command: 7 if failed in command else 0
                ) as run,
            ):
                with self.assertRaises(SystemExit) as result:
                    PLAN.main()
                self.assertEqual(result.exception.code, 7)
                self.assertEqual(run.call_count, 2)

    def test_core_selection_rejects_missing_or_duplicate_coverage(self):
        package = self.package("pkg/core", ["TestLifecycle", "TestOther"])
        manifest = {"packages": [], "selected": {"./pkg/core": ["TestLifecycle"]}}
        self.assertEqual(
            PLAN.core_plan([package], manifest),
            ([], [("./pkg/core", ["TestLifecycle"])]),
        )
        for invalid in [
            {"packages": ["./pkg/missing"], "selected": {}},
            {"packages": ["./pkg/core", "./pkg/core"], "selected": {}},
            {"packages": ["./pkg/core"], "selected": {"./pkg/core": ["TestLifecycle"]}},
            {"packages": [], "selected": {"./pkg/core": ["TestRemoved"]}},
            {"packages": [], "selected": {"./pkg/core": []}},
            {"packages": [], "selected": {"./pkg/core": ["TestLifecycle", "TestLifecycle"]}},
        ]:
            with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                PLAN.core_plan([package], invalid)

    def test_core_uses_active_files_and_keeps_new_tests_in_full_packages(self):
        package = self.package("pkg/core", ["TestExisting", "TestNew"])
        (Path(package["Dir"]) / "case_other_test.go").write_text("func TestInactive() {}")
        self.assertEqual(
            PLAN.core_plan([package], {"packages": ["./pkg/core"], "selected": {}}),
            (["./pkg/core"], []),
        )
        with self.assertRaisesRegex(ValueError, "TestInactive"):
            PLAN.core_plan(
                [package], {"packages": [], "selected": {"./pkg/core": ["TestInactive"]}}
            )

    def test_core_failure_in_any_parallel_group_fails_the_check(self):
        packages = [
            self.package("pkg/core", ["TestCore"]),
            self.package("internal/control", ["TestLifecycle", "TestOther"]),
        ]
        manifest = self.root / "core.json"
        manifest.write_text(
            json.dumps(
                {
                    "packages": ["./pkg/core"],
                    "selected": {"./internal/control": ["TestLifecycle"]},
                }
            )
        )
        for failed in ["./pkg/core", "./internal/control"]:
            with (
                self.subTest(failed=failed),
                patch.object(PLAN, "CORE_MANIFEST", manifest),
                patch.object(PLAN, "normal_only", return_value={}),
                patch.object(PLAN.sys, "argv", ["test_go.py", "core"]),
                patch.object(
                    PLAN.subprocess,
                    "check_output",
                    return_value="\n".join(json.dumps(package) for package in packages),
                ),
                patch.object(
                    PLAN, "run", side_effect=lambda command: 7 if failed in command else 0
                ) as run,
            ):
                with self.assertRaises(SystemExit) as result:
                    PLAN.main()
                self.assertEqual(result.exception.code, 7)
                selected = next(
                    call.args[0]
                    for call in run.call_args_list
                    if "./internal/control" in call.args[0]
                )
                self.assertEqual(selected[selected.index("-run") + 1], "^(TestLifecycle)$")
                self.assertEqual(run.call_count, 2)

    def test_shard_arguments_reject_invalid_or_out_of_range_values(self):
        self.assertEqual(PLAN.parse_shard("2/4"), (2, 4))
        for value in ["0/4", "1/0", "5/4", "-1/4", "1", "1/2/3", "01/4"]:
            with self.subTest(value=value), self.assertRaises(PLAN.argparse.ArgumentTypeError):
                PLAN.parse_shard(value)

    def test_package_shards_are_disjoint_complete_and_order_independent(self):
        packages = [
            self.package("pkg/large", [f"TestCase{i}" for i in range(12)]),
            self.package("pkg/small", ["TestSmall"]),
            self.package("pkg/external", ["TestInternal"]),
        ]
        external = Path(packages[-1]["Dir"])
        (external / "external_test.go").write_text("func TestExternal() {}")
        packages[-1]["XTestGoFiles"] = ["external_test.go"]
        (external / "inactive_test.go").write_text("func TestInactive() {}")
        empty = self.root / "pkg/no-tests"
        empty.mkdir()
        packages.append({"Dir": str(empty)})
        chosen = []
        for index in range(1, 5):
            shard = PLAN.shard_packages(packages, (index, 4))
            self.assertEqual(
                {p["Dir"] for p in shard},
                {p["Dir"] for p in PLAN.shard_packages(list(reversed(packages)), (index, 4))},
            )
            chosen.extend(p["Dir"] for p in shard)
        self.assertCountEqual(chosen, [p["Dir"] for p in packages])
        self.assertEqual(len(chosen), len(set(chosen)))
        self.assertEqual(PLAN.shard_packages(packages[:1], (4, 4)), [])

    def test_all_shards_preserve_normal_and_race_coverage_and_flags(self):
        packages = [
            self.package("pkg/full", ["TestFull"]),
            self.package("pkg/mixed", ["TestConcurrent", "TestSerial"]),
            self.package("pkg/serial", ["TestValue"]),
        ]
        exclusions = {"./pkg/mixed": {"TestSerial"}, "./pkg/serial": {"TestValue"}}

        def coverage(mode, shard):
            with (
                patch.object(PLAN, "normal_only", return_value=exclusions),
                patch.object(PLAN.sys, "argv", ["test_go.py", mode, "--shard", shard, "--count=1"]),
                patch.object(
                    PLAN.subprocess,
                    "check_output",
                    return_value="\n".join(json.dumps(p) for p in packages),
                ),
                patch.object(PLAN, "run", return_value=0) as run,
            ):
                try:
                    PLAN.main()
                except SystemExit as result:
                    self.assertEqual(result.code, 0)
            covered = []
            for call in run.call_args_list:
                command = call.args[0]
                self.assertIn("-count=1", command)
                pattern = command[command.index("-run") + 1] if "-run" in command else None
                covered.extend(
                    (package, pattern, "-race" in command)
                    for package in command
                    if package.startswith("./")
                )
            return covered

        for mode in ["normal", "race", "race-all"]:
            with self.subTest(mode=mode):
                original = coverage(mode, "1/1")
                split = [entry for i in range(1, 5) for entry in coverage(mode, f"{i}/4")]
                self.assertCountEqual(original, split)
                self.assertEqual(len(split), len(set(split)))


if __name__ == "__main__":
    unittest.main()
