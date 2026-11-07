from pathlib import Path
from tempfile import TemporaryDirectory
import unittest
from zipfile import ZipFile

from detect import contains_flutter


class PackageDetection(unittest.TestCase):
    def test_android_abi_split(self):
        with TemporaryDirectory() as tmp:
            base = Path(tmp) / "base.apk"
            abi = Path(tmp) / "arm64.apk"
            with ZipFile(base, "w") as archive:
                archive.writestr("assets/flutter_assets/AssetManifest.bin", b"")
            with ZipFile(abi, "w") as archive:
                archive.writestr("lib/arm64-v8a/libflutter.so", b"")
            self.assertFalse(contains_flutter([base]))
            self.assertFalse(contains_flutter([abi]))
            self.assertTrue(contains_flutter([base, abi]))

    def test_ios_requires_assets_in_the_same_app(self):
        with TemporaryDirectory() as tmp:
            ipa = Path(tmp) / "example.ipa"
            with ZipFile(ipa, "w") as archive:
                archive.writestr("Payload/Main.app/Frameworks/Flutter.framework/Flutter", b"")
                archive.writestr("Payload/Other.app/Frameworks/App.framework/flutter_assets/x", b"")
            self.assertFalse(contains_flutter([ipa]))
            with ZipFile(ipa, "a") as archive:
                archive.writestr("Payload/Main.app/Frameworks/App.framework/flutter_assets/x", b"")
            self.assertTrue(contains_flutter([ipa]))

    def test_simulator_bundle(self):
        with TemporaryDirectory() as tmp:
            app = Path(tmp) / "Runner.app"
            engine = app / "Frameworks/Flutter.framework/Flutter"
            engine.parent.mkdir(parents=True)
            engine.touch()
            self.assertFalse(contains_flutter([app]))
            asset = app / "Frameworks/App.framework/flutter_assets/AssetManifest.bin"
            asset.parent.mkdir(parents=True)
            asset.touch()
            self.assertTrue(contains_flutter([app]))
