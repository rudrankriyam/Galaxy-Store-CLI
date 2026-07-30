#!/usr/bin/env python3
"""Generate candidate WinGet manifests from a local Windows snapshot."""

from __future__ import annotations

import argparse
import hashlib
import re
from pathlib import Path
from urllib.parse import urlparse


PACKAGE_IDENTIFIER = "RudrankRiyam.GalaxyStoreCLI"
PACKAGE_PUBLISHER = "Rudrank Riyam"
PACKAGE_NAME = "Galaxy Store CLI"
PACKAGE_COMMAND = "gsc"
REPOSITORY_URL = "https://github.com/rudrankriyam/Galaxy-Store-CLI"
# windows-latest currently ships a WinGet client whose schema validator accepts
# the 1.10 schema headers. Keep candidate generation pinned to that validator
# until the workflow explicitly installs and pins a newer client.
MANIFEST_VERSION = "1.10.0"
SUPPORTED_ARCHITECTURES = ("x64", "arm64")


def schema_header(manifest_type: str) -> str:
    return (
        "# yaml-language-server: $schema="
        f"https://aka.ms/winget-manifest.{manifest_type}.{MANIFEST_VERSION}.schema.json"
    )


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Generate non-publishing WinGet manifests from a local gsc.exe "
            "snapshot. This command never submits manifests."
        )
    )
    parser.add_argument("--version", required=True, help="Candidate package version")
    parser.add_argument(
        "--installer",
        required=True,
        type=Path,
        help="Path to the local gsc.exe snapshot used to calculate SHA256",
    )
    parser.add_argument(
        "--installer-url",
        required=True,
        help="Candidate HTTPS URL WinGet would use for this exact binary",
    )
    parser.add_argument(
        "--architecture",
        choices=SUPPORTED_ARCHITECTURES,
        default="x64",
        help="Windows target architecture (default: x64)",
    )
    parser.add_argument(
        "--output-dir",
        required=True,
        type=Path,
        help="Root directory where the winget-pkgs-style manifest tree is written",
    )
    return parser.parse_args()


def validate_version(version: str) -> str:
    normalized = version.strip()
    if not re.fullmatch(
        r"\d+\.\d+\.\d+(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?", normalized
    ):
        raise ValueError(
            f"version must be semver-like x.y.z or x.y.z-suffix, got {version!r}"
        )
    return normalized


def validate_installer(installer: Path) -> Path:
    resolved = installer.resolve()
    if not resolved.is_file():
        raise ValueError(f"installer does not exist: {installer}")
    if resolved.name.casefold() != "gsc.exe":
        raise ValueError(
            f"snapshot installer must be named gsc.exe, got {resolved.name!r}"
        )
    return resolved


def validate_installer_url(installer_url: str) -> str:
    normalized = installer_url.strip()
    parsed = urlparse(normalized)
    if parsed.scheme != "https" or not parsed.netloc:
        raise ValueError("installer URL must be an absolute HTTPS URL")
    if parsed.username or parsed.password:
        raise ValueError("installer URL must not contain credentials")
    if not parsed.path.casefold().endswith(".exe"):
        raise ValueError("installer URL must identify a Windows .exe asset")
    return normalized


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest().upper()


def manifest_directory(output_dir: Path, version: str) -> Path:
    return (
        output_dir
        / "manifests"
        / "r"
        / "RudrankRiyam"
        / "GalaxyStoreCLI"
        / version
    )


def write_manifest(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8", newline="\n")


def generate(
    *,
    version: str,
    installer: Path,
    installer_url: str,
    architecture: str,
    output_dir: Path,
) -> list[Path]:
    version = validate_version(version)
    installer = validate_installer(installer)
    installer_url = validate_installer_url(installer_url)
    if architecture not in SUPPORTED_ARCHITECTURES:
        raise ValueError(f"unsupported architecture: {architecture}")

    target = manifest_directory(output_dir.resolve(), version)
    version_file = target / f"{PACKAGE_IDENTIFIER}.yaml"
    locale_file = target / f"{PACKAGE_IDENTIFIER}.locale.en-US.yaml"
    installer_file = target / f"{PACKAGE_IDENTIFIER}.installer.yaml"
    checksum = sha256_file(installer)

    write_manifest(
        version_file,
        f"""{schema_header("version")}
# Generated locally; this file is not a submission.
PackageIdentifier: {PACKAGE_IDENTIFIER}
PackageVersion: {version}
DefaultLocale: en-US
ManifestType: version
ManifestVersion: {MANIFEST_VERSION}
""",
    )
    write_manifest(
        locale_file,
        f"""{schema_header("defaultLocale")}
# Generated locally; this file is not a submission.
PackageIdentifier: {PACKAGE_IDENTIFIER}
PackageVersion: {version}
PackageLocale: en-US
Publisher: {PACKAGE_PUBLISHER}
PublisherUrl: https://github.com/rudrankriyam
PackageName: {PACKAGE_NAME}
PackageUrl: {REPOSITORY_URL}
License: MIT
LicenseUrl: {REPOSITORY_URL}/blob/main/LICENSE
Copyright: Copyright (c) Rudrank Riyam
ShortDescription: Unofficial, automation-first CLI for the Galaxy Store Developer API.
Moniker: {PACKAGE_COMMAND}
Tags:
  - android
  - cli
  - galaxy-store
  - samsung
ManifestType: defaultLocale
ManifestVersion: {MANIFEST_VERSION}
""",
    )
    write_manifest(
        installer_file,
        f"""{schema_header("installer")}
# Generated locally; this file is not a submission.
PackageIdentifier: {PACKAGE_IDENTIFIER}
PackageVersion: {version}
InstallerLocale: en-US
Platform:
  - Windows.Desktop
InstallerType: portable
UpgradeBehavior: install
Commands:
  - {PACKAGE_COMMAND}
Installers:
  - Architecture: {architecture}
    InstallerUrl: {installer_url}
    InstallerSha256: {checksum}
ManifestType: installer
ManifestVersion: {MANIFEST_VERSION}
""",
    )
    return [version_file, locale_file, installer_file]


def main() -> None:
    args = parse_args()
    try:
        generated = generate(
            version=args.version,
            installer=args.installer,
            installer_url=args.installer_url,
            architecture=args.architecture,
            output_dir=args.output_dir,
        )
    except ValueError as error:
        raise SystemExit(str(error)) from error

    for path in generated:
        print(path)


if __name__ == "__main__":
    main()
