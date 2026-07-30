import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
FULL_SHA = re.compile(r"^[0-9a-f]{40}$")


class ConsumerActionTests(unittest.TestCase):
    def test_action_builds_source_without_secret_inputs_or_release_downloads(self):
        action = (ROOT / "action.yml").read_text(encoding="utf-8")

        self.assertNotIn("\ninputs:", action)
        self.assertNotIn("secrets.", action)
        self.assertNotIn("curl ", action)
        self.assertNotIn("wget ", action)
        self.assertNotIn("releases/download", action)
        self.assertIn("${{ github.action_path }}/go.mod", action)
        self.assertIn(".github/actions/setup-gsc/build.sh", action)
        self.assertIn(".github/actions/setup-gsc/build.ps1", action)

        build_scripts = "\n".join(
            (
                (ROOT / ".github/actions/setup-gsc/build.sh").read_text(
                    encoding="utf-8"
                ),
                (ROOT / ".github/actions/setup-gsc/build.ps1").read_text(
                    encoding="utf-8"
                ),
            )
        )
        self.assertIn("-buildvcs=false", build_scripts)
        self.assertIn("-mod=readonly", build_scripts)
        self.assertIn("GOWORK", build_scripts)

    def test_action_third_party_uses_are_full_commit_shas(self):
        action = (ROOT / "action.yml").read_text(encoding="utf-8")
        uses = re.findall(r"^\s*uses:\s*([^@\s]+)@([^\s#]+)", action, re.MULTILINE)

        self.assertTrue(uses)
        for repository, ref in uses:
            with self.subTest(repository=repository):
                self.assertRegex(ref, FULL_SHA)

    def test_consumer_example_requires_a_reviewed_full_commit(self):
        documentation = (ROOT / "docs/GITHUB_ACTION.md").read_text(
            encoding="utf-8"
        )

        references = re.findall(
            r"rudrankriyam/Galaxy-Store-CLI@([^\s]+)",
            documentation,
        )
        self.assertTrue(references)
        for reference in references:
            self.assertRegex(reference, FULL_SHA)
        self.assertNotIn("Galaxy-Store-CLI@main", documentation)
        self.assertNotIn("Galaxy-Store-CLI@v", documentation)
        self.assertIn("does not download a release", documentation)

    def test_smoke_workflow_consumes_local_action_and_runs_catalog_commands(self):
        workflow = (
            ROOT / ".github/workflows/action-smoke.yml"
        ).read_text(encoding="utf-8")

        self.assertIn("uses: ./", workflow)
        self.assertIn("gsc version", workflow)
        self.assertIn("gsc capabilities --output json", workflow)
        self.assertIn("gsc schema --output json", workflow)
        self.assertNotIn("secrets.", workflow)
        self.assertNotIn("releases/download", workflow)

        remote_uses = re.findall(
            r"^\s*uses:\s*([^./][^@\s]+)@([^\s#]+)", workflow, re.MULTILINE
        )
        self.assertTrue(remote_uses)
        for repository, ref in remote_uses:
            with self.subTest(repository=repository):
                self.assertRegex(ref, FULL_SHA)


if __name__ == "__main__":
    unittest.main()
