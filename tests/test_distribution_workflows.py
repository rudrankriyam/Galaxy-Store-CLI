from __future__ import annotations

import re
import unittest
from pathlib import Path


REPOSITORY_ROOT = Path(__file__).resolve().parents[1]
WORKFLOWS = (
    REPOSITORY_ROOT / ".github/workflows/distribution-snapshot.yml",
    REPOSITORY_ROOT / ".github/workflows/draft-release.yml",
    REPOSITORY_ROOT / ".github/workflows/winget-snapshot.yml",
)


class DistributionWorkflowTests(unittest.TestCase):
    def test_external_actions_are_pinned_to_full_commit_shas(self) -> None:
        for path in WORKFLOWS:
            with self.subTest(workflow=path.name):
                uses = re.findall(r"(?m)^\s*uses:\s*(\S+)", path.read_text())
                self.assertTrue(uses, f"{path.name} must declare its actions")
                for action in uses:
                    self.assertRegex(
                        action,
                        r"^[^@]+@[0-9a-f]{40}$",
                        f"{path.name} has an unpinned action: {action}",
                    )

    def test_snapshot_workflows_remain_read_only_and_non_publishing(self) -> None:
        for name in ("distribution-snapshot.yml", "winget-snapshot.yml"):
            path = REPOSITORY_ROOT / ".github/workflows" / name
            workflow = path.read_text()
            with self.subTest(workflow=name):
                self.assertIn("permissions:\n  contents: read", workflow)
                self.assertNotIn("${{ github.token }}", workflow)
                for forbidden in (
                    "gh release create",
                    "git tag ",
                    "git push",
                    "wingetcreate submit",
                ):
                    self.assertNotIn(forbidden, workflow)

    def test_draft_release_requires_existing_tag_and_typed_confirmation(self) -> None:
        workflow = (
            REPOSITORY_ROOT / ".github/workflows/draft-release.yml"
        ).read_text()
        config = (REPOSITORY_ROOT / ".goreleaser.yaml").read_text()

        self.assertIn('test "$RELEASE_CONFIRMATION" = "CREATE_DRAFT_RELEASE"', workflow)
        self.assertIn(
            'git rev-parse --verify "refs/tags/${RELEASE_TAG}^{commit}"',
            workflow,
        )
        self.assertIn(
            'test "$(git rev-parse HEAD)" = "$(git rev-list -n 1 "$RELEASE_TAG")"',
            workflow,
        )
        self.assertNotIn("git tag ", workflow)
        self.assertNotIn("git push", workflow)
        self.assertRegex(config, r"(?m)^release:\n  draft: true$")


if __name__ == "__main__":
    unittest.main()
