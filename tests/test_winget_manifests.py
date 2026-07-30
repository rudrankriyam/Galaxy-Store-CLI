from __future__ import annotations

import importlib.util
import sys
import tempfile
import unittest
from pathlib import Path


REPOSITORY_ROOT = Path(__file__).resolve().parents[1]
SCRIPTS_DIR = REPOSITORY_ROOT / "scripts"


def load_module(name: str, path: Path):
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"unable to load {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


generator = load_module(
    "generate_winget_manifests", SCRIPTS_DIR / "generate_winget_manifests.py"
)
validator = load_module(
    "validate_winget_manifests", SCRIPTS_DIR / "validate_winget_manifests.py"
)


class WingetManifestTests(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tempdir.cleanup)
        self.root = Path(self.tempdir.name)
        self.installer = self.root / "gsc.exe"
        self.installer.write_bytes(b"candidate Windows snapshot\n")

    def generate(self, architecture: str = "x64") -> list[Path]:
        return generator.generate(
            version="0.0.0-snapshot",
            installer=self.installer,
            installer_url="https://example.invalid/snapshots/gsc.exe",
            architecture=architecture,
            output_dir=self.root / "winget",
        )

    def test_generates_three_valid_portable_manifests(self) -> None:
        generated = self.generate()
        self.assertEqual(len(generated), 3)
        self.assertEqual(generator.MANIFEST_VERSION, "1.10.0")
        manifest_dir = generated[0].parent

        validated = validator.validate(manifest_dir, self.installer)

        self.assertEqual(sorted(generated), validated)
        installer_text = generated[2].read_text(encoding="utf-8")
        self.assertIn("PackageIdentifier: RudrankRiyam.GalaxyStoreCLI", installer_text)
        self.assertIn("InstallerType: portable", installer_text)
        self.assertNotIn("\nScope:", installer_text)
        self.assertIn("Commands:\n  - gsc", installer_text)
        for path, manifest_type in zip(
            generated, ("version", "defaultLocale", "installer"), strict=True
        ):
            self.assertEqual(
                path.read_text(encoding="utf-8").splitlines()[0],
                generator.schema_header(manifest_type),
            )
        self.assertIn(
            f"InstallerSha256: {generator.sha256_file(self.installer)}",
            installer_text,
        )

    def test_generates_and_validates_each_supported_architecture(self) -> None:
        for architecture in generator.SUPPORTED_ARCHITECTURES:
            with self.subTest(architecture=architecture):
                output_dir = self.root / architecture
                generated = generator.generate(
                    version="0.0.0-snapshot",
                    installer=self.installer,
                    installer_url=(
                        "https://example.invalid/snapshots/"
                        f"{architecture}/gsc.exe"
                    ),
                    architecture=architecture,
                    output_dir=output_dir,
                )

                validator.validate(generated[0].parent, self.installer)
                installer_text = generated[2].read_text(encoding="utf-8")
                self.assertIn(f"Architecture: {architecture}", installer_text)

    def test_rejects_an_installer_with_the_wrong_command_name(self) -> None:
        wrong_installer = self.root / "galaxy-store.exe"
        wrong_installer.write_bytes(b"wrong name")

        with self.assertRaisesRegex(ValueError, "named gsc.exe"):
            generator.generate(
                version="0.0.0-snapshot",
                installer=wrong_installer,
                installer_url="https://example.invalid/gsc.exe",
                architecture="x64",
                output_dir=self.root / "winget",
            )

    def test_rejects_a_tampered_checksum(self) -> None:
        generated = self.generate()
        installer_manifest = generated[2]
        text = installer_manifest.read_text(encoding="utf-8")
        checksum = generator.sha256_file(self.installer)
        installer_manifest.write_text(
            text.replace(checksum, "0" * 64), encoding="utf-8"
        )

        with self.assertRaisesRegex(ValueError, "InstallerSha256 mismatch"):
            validator.validate(generated[0].parent, self.installer)

    def test_rejects_non_https_candidate_urls(self) -> None:
        with self.assertRaisesRegex(ValueError, "absolute HTTPS"):
            generator.generate(
                version="0.0.0-snapshot",
                installer=self.installer,
                installer_url="http://example.invalid/gsc.exe",
                architecture="x64",
                output_dir=self.root / "winget",
            )


if __name__ == "__main__":
    unittest.main()
