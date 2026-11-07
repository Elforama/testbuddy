"""Run all mobile flows five times on an installed Flutter benchmark."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import sys
import time
import xml.etree.ElementTree as ET

root = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(root / "adapters/flutter"))
from detect import contains_flutter

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("platform", choices=["ios", "android"])
parser.add_argument("device")
parser.add_argument("output", type=Path)
args = parser.parse_args()
out = args.output.resolve()
out.mkdir(parents=True, exist_ok=False)
flows = sorted((root / "bench/flows/scenarios").glob("*.yaml"))
flows.extend(sorted((root / "bench/flutter/flows/regressions").glob("*.yaml")))
flows.append(root / "bench/flows/journey.yaml")
if args.platform == "android":
    flows.append(root / "bench/flows/kill-app-android.yaml")
binary = out / "tb"
shutil.copy2(root / "bin/tb", binary)
app = "dev.testbuddy.bench.flutter"
artifact = root / "bench/flutter/build" / (
    "app/outputs/flutter-apk/app-debug.apk" if args.platform == "android"
    else "ios/iphonesimulator/Runner.app/Frameworks/App.framework/flutter_assets/kernel_blob.bin"
)
app_package = root / "bench/flutter/build/ios/iphonesimulator/Runner.app" if args.platform == "ios" else artifact
if not contains_flutter([app_package]):
    raise SystemExit(f"Flutter engine or assets missing in {app_package}")
manifest = {
    "platform": args.platform, "device": args.device, "app_id": app,
    "artifact": str(artifact.relative_to(root)),
    "artifact_sha256": hashlib.sha256(artifact.read_bytes()).hexdigest(),
    "runner_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
    "base_commit": subprocess.check_output(["git","rev-parse","HEAD"], cwd=root, text=True).strip(),
}
(out / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
results = []
for n in range(1, 6):
    folder = out / f"round-{n}"
    folder.mkdir()
    report = folder / "report.xml"
    cmd = [str(binary), "test", *map(str, flows), "-p", args.platform, "--device", args.device,
           "--wait-for-device", "3m", "-e", f"BENCH={app}", "-e", "BENCH_SCHEME=benchflutter", "--format", "junit",
           "--output", str(report), "--debug-output", str(folder / "debug")]
    start = time.monotonic()
    with (folder / "runner.log").open("w") as log:
        code = subprocess.run(cmd, cwd=root, stdout=log, stderr=subprocess.STDOUT).returncode
    cases = []
    if report.exists():
        for case in ET.parse(report).iter("testcase"):
            cases.append({"flow": case.get("name"), "status": case.get("status"),
                          "seconds": float(case.get("time", 0)),
                          "errors": [e.text or e.get("message") for e in case if e.tag in ("error","failure","skipped")]})
    results.append({"round": n, "exit_code": code, "wall_seconds": round(time.monotonic()-start,2),
                    "expected": len(flows), "reported": len(cases), "cases": cases})
    (out / "summary.json").write_text(json.dumps(results, indent=2) + "\n")
    print(f"{args.platform} round {n}: {sum(c['status']=='SUCCESS' for c in cases)}/{len(flows)} passed", flush=True)
