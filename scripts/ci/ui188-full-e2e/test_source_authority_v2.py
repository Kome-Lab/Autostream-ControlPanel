import copy
import unittest

from source_v2 import (
    AUTHORITY_PATH,
    Blocked,
    unique_json,
    validate_authority,
)


class SourceAuthorityV2Tests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.authority = unique_json(AUTHORITY_PATH.read_bytes())

    def test_candidate_authority_has_exact_component_partition(self):
        components = validate_authority(self.authority)
        self.assertEqual(set(components), {
            "runtime-source", "test-source", "browser-test-harness", "database-schema",
            "ci-integration-harness", "workflow-policy", "build-packaging-config",
            "dependency-lock", "documentation", "repository-governance",
            "source-governance", "source-governance-tests", "web-tooling",
            "diagnostic-excluded",
        })
        self.assertEqual(sum(c["file_count"] for c in components.values()), 1687)

    def test_reject_duplicate_path_across_components(self):
        value = copy.deepcopy(self.authority)
        row = copy.deepcopy(value["components"]["runtime-source"]["files"][0])
        target = value["components"]["test-source"]
        target["files"].append(row)
        target["files"].sort(key=lambda item: item["path"])
        target["file_count"] = len(target["files"])
        with self.assertRaisesRegex(Blocked, "BLOCKED_V2_COMPONENT_OVERLAP"):
            validate_authority(value)

    def test_reject_path_traversal(self):
        value = copy.deepcopy(self.authority)
        row = value["components"]["runtime-source"]["files"][0]
        row["path"] = "../escape.go"
        with self.assertRaisesRegex(Blocked, "BLOCKED_V2_PATH"):
            validate_authority(value)

    def test_reject_changed_component_tree(self):
        value = copy.deepcopy(self.authority)
        value["components"]["runtime-source"]["tree_sha1"] = "0" * 40
        with self.assertRaisesRegex(Blocked, "BLOCKED_V2_COMPONENT_TREE"):
            validate_authority(value)

    def test_reject_relaxed_acceptance_contract(self):
        value = copy.deepcopy(self.authority)
        value["acceptance"]["two_stream_overlap_seconds"] = 999
        with self.assertRaisesRegex(Blocked, "BLOCKED_V2_ACCEPTANCE_RELAXED"):
            validate_authority(value)

    def test_reject_unapproved_diagnostic_exclusion_change(self):
        value = copy.deepcopy(self.authority)
        value["excluded_components"] = []
        with self.assertRaisesRegex(Blocked, "BLOCKED_V2_EXCLUSION_POLICY"):
            validate_authority(value)


if __name__ == "__main__":
    unittest.main()
