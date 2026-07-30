#!/usr/bin/env python3
"""Validate Galaxy Store CLI WinGet manifest identity and snapshot integrity."""

from __future__ import annotations

import argparse
import re
from pathlib import Path

from generate_winget_manifests import (
    MANIFEST_VERSION,
    PACKAGE_COMMAND,
    PACKAGE_IDENTIFIER,
    SUPPORTED_ARCHITECTURES,
    schema_header,
    sha256_file,
    validate_installer,
    validate_installer_url,
    validate_version,
)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Validate generated candidate manifests without submitting them."
    )
    parser.add_argument("--manifest-dir", required=True, type=Path)
    parser.add_argument(
        "--installer",
        type=Path,
        help="Optional local gsc.exe whose SHA256 must match the installer manifest",
    )
    return parser.parse_args()


def scalar(text: str, key: str) -> str:
    matches = re.findall(
        rf"(?m)^[ \t]*{re.escape(key)}:\s*(\S(?:.*\S)?)\s*$", text
    )
    if len(matches) != 1:
        raise ValueError(f"expected exactly one {key} field, found {len(matches)}")
    return matches[0].strip("\"'")


def sequence(text: str, key: str) -> list[str]:
    lines = text.splitlines()
    header = f"{key}:"
    for index, line in enumerate(lines):
        if line == header:
            values: list[str] = []
            for item in lines[index + 1 :]:
                match = re.fullmatch(r"\s{2}-\s+(.+?)\s*", item)
                if match:
                    values.append(match.group(1).strip("\"'"))
                    continue
                if item.startswith(" "):
                    continue
                break
            return values
    raise ValueError(f"missing {key} sequence")


def installer_architecture(text: str) -> str:
    matches = re.findall(
        r"(?m)^[ \t]*-\s+Architecture:\s*(\S+)\s*$",
        text,
    )
    if len(matches) != 1:
        raise ValueError(
            "expected exactly one installer Architecture field, "
            f"found {len(matches)}"
        )
    return matches[0].strip("\"'")


def validate(manifest_dir: Path, installer: Path | None = None) -> list[Path]:
    manifest_dir = manifest_dir.resolve()
    expected = {
        "version": manifest_dir / f"{PACKAGE_IDENTIFIER}.yaml",
        "defaultLocale": (
            manifest_dir / f"{PACKAGE_IDENTIFIER}.locale.en-US.yaml"
        ),
        "installer": manifest_dir / f"{PACKAGE_IDENTIFIER}.installer.yaml",
    }
    unexpected = sorted(manifest_dir.glob("*.yaml"))
    expected_paths = sorted(expected.values())
    if unexpected != expected_paths:
        raise ValueError(
            "manifest directory must contain exactly the version, en-US locale, "
            "and installer manifests"
        )

    texts = {
        kind: path.read_text(encoding="utf-8") for kind, path in expected.items()
    }
    versions: set[str] = set()
    for kind, text in texts.items():
        if text.splitlines()[0] != schema_header(kind):
            raise ValueError(f"{kind} manifest has the wrong schema header")
        if scalar(text, "PackageIdentifier") != PACKAGE_IDENTIFIER:
            raise ValueError(f"{kind} manifest has the wrong PackageIdentifier")
        versions.add(validate_version(scalar(text, "PackageVersion")))
        if scalar(text, "ManifestType") != kind:
            raise ValueError(f"{kind} manifest has the wrong ManifestType")
        if scalar(text, "ManifestVersion") != MANIFEST_VERSION:
            raise ValueError(f"{kind} manifest has the wrong ManifestVersion")
    if len(versions) != 1:
        raise ValueError("PackageVersion must match across all manifests")

    if scalar(texts["version"], "DefaultLocale") != "en-US":
        raise ValueError("DefaultLocale must be en-US")
    if scalar(texts["defaultLocale"], "PackageLocale") != "en-US":
        raise ValueError("PackageLocale must be en-US")
    if scalar(texts["defaultLocale"], "Moniker") != PACKAGE_COMMAND:
        raise ValueError(f"Moniker must be {PACKAGE_COMMAND}")

    installer_text = texts["installer"]
    if scalar(installer_text, "InstallerType") != "portable":
        raise ValueError("InstallerType must be portable")
    architecture = installer_architecture(installer_text)
    if architecture not in SUPPORTED_ARCHITECTURES:
        raise ValueError(
            "Architecture must be one of "
            + ", ".join(SUPPORTED_ARCHITECTURES)
        )
    if sequence(installer_text, "Commands") != [PACKAGE_COMMAND]:
        raise ValueError(f"Commands must contain only {PACKAGE_COMMAND}")

    validate_installer_url(scalar(installer_text, "InstallerUrl"))

    checksum = scalar(installer_text, "InstallerSha256")
    if not re.fullmatch(r"[A-F0-9]{64}", checksum):
        raise ValueError("InstallerSha256 must be 64 uppercase hexadecimal characters")
    if installer is not None:
        actual = sha256_file(validate_installer(installer))
        if checksum != actual:
            raise ValueError(
                f"InstallerSha256 mismatch: manifest={checksum}, local={actual}"
            )

    return expected_paths


def main() -> None:
    args = parse_args()
    try:
        validated = validate(args.manifest_dir, args.installer)
    except (OSError, ValueError) as error:
        raise SystemExit(str(error)) from error
    for path in validated:
        print(path)


if __name__ == "__main__":
    main()
