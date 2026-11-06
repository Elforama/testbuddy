"""Identify a packaged Flutter engine and assets. An app can also contain native screens."""
import argparse
from pathlib import Path
import re
import zipfile


def contains_flutter(paths: list[Path]) -> bool:
    """Pass one .app, one .ipa, or all APK splits from a single installation."""
    if len(paths) == 1 and paths[0].is_dir():
        frameworks = paths[0] / "Frameworks"
        return ((frameworks / "Flutter.framework/Flutter").is_file()
                and any(f.is_file() for f in (frameworks / "App.framework/flutter_assets").rglob("*")))
    if len(paths) == 1 and paths[0].suffix.lower() == ".ipa":
        with zipfile.ZipFile(paths[0]) as archive:
            files = set(archive.namelist())
            for name in files:
                match = re.fullmatch(r"(Payload/[^/]+\.app/Frameworks/)Flutter\.framework/Flutter", name)
                if match and any(f.startswith(match[1] + "App.framework/flutter_assets/") and not f.endswith("/") for f in files):
                    return True
        return False
    engine = assets = False
    for path in paths:
        if path.suffix.lower() != ".apk":
            raise ValueError("expected one .app, one .ipa, or the APK splits of one application")
        with zipfile.ZipFile(path) as archive:
            names = archive.namelist()
            engine |= any(re.fullmatch(r"lib/[^/]+/libflutter\.so", f) is not None for f in names)
            assets |= any(f.startswith("assets/flutter_assets/") and not f.endswith("/") for f in names)
    return engine and assets


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("paths", type=Path, nargs="+")
    args = parser.parse_args()
    try:
        found = contains_flutter(args.paths)
    except (OSError, ValueError, zipfile.BadZipFile) as error:
        parser.exit(2, f"{error}\n")
    print("Flutter" if found else "No Flutter engine and asset pair")
    raise SystemExit(0 if found else 1)
