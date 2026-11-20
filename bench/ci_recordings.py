#!/usr/bin/env python3
"""CI annotated recordings: the bench journey as a video a person can follow, on the pull request.

On a pull request each e2e job runs the journey once more with `--record --annotate`, apart
from the timed suite, into build/e2e/annotated/<platform> (part of its artifact). The
`recordings` job downloads the artifacts and runs this script twice:

  bench/ci_recordings.py prepare --artifacts build/artifacts --out build/recordings
      finds each platform's recording.mp4, and puts in <out>/<platform>/ the video, its captions
      (recording.srt), a GIF preview small enough to show in a comment, and what --annotate
      logged about the video (its sync check);

  bench/ci_recordings.py comment --out build/recordings --repo OWNER/REPO --sha SHA --run-url URL
      writes <out>/comment.md: the pull request comment, with the previews inline and links to
      the videos, at commit SHA of the branch the workflow pushed <out> to.

GitHub plays a video in a comment only when a person uploads it in the browser, so the comment
shows GIF previews and links to the full videos.
"""
import argparse
import glob
import os
import shutil
import subprocess
import sys

PLATFORMS = ["android", "ios", "web"]
MARKER = "<!-- tb-annotated-recordings -->"
# GitHub shows images in comments up to 10 MB; previews are kept under this.
GIF_LIMIT = 9 * 1024 * 1024
# (frames per second, width of a portrait video, of a landscape one), tried in order until the
# preview fits.
GIF_TRIES = [(8, 320, 640), (6, 270, 540), (4, 220, 440)]


def video_size(path):
    out = subprocess.run(["ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries",
                          "stream=width,height", "-of", "csv=p=0", path],
                         capture_output=True, text=True, check=True).stdout.strip()
    w, h = out.split(",")[:2]
    return int(w), int(h)


def make_gif(src, dst):
    w, h = video_size(src)
    for fps, portrait, landscape in GIF_TRIES:
        width = portrait if h > w else landscape
        vf = (f"fps={fps},scale={width}:-2:flags=lanczos,split[a][b];"
              "[a]palettegen=stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=4")
        subprocess.run(["ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-i", src, "-vf", vf,
                        "-loop", "0", dst], check=True)
        if os.path.getsize(dst) <= GIF_LIMIT:
            return True
    os.remove(dst)
    return False


def annotate_lines(log):
    """What --annotate said about the video (not its holds), from the flow's log.txt."""
    if not os.path.exists(log):
        return []
    lines = []
    with open(log, encoding="utf-8", errors="replace") as f:
        for line in f:
            if "--annotate:" in line and "holding the screen" not in line:
                lines.append(line.split("--annotate:", 1)[1].strip())
    return lines


def prepare(args):
    os.makedirs(args.out, exist_ok=True)
    found = 0
    for platform in PLATFORMS:
        videos = sorted(glob.glob(os.path.join(args.artifacts, "*", "annotated", platform, "**", "recording*.mp4"),
                                  recursive=True))
        if not videos:
            print(f"{platform}: no annotated recording")
            continue
        src = videos[0]
        dst = os.path.join(args.out, platform)
        os.makedirs(dst, exist_ok=True)
        shutil.copy(src, os.path.join(dst, "recording.mp4"))
        srt = os.path.splitext(src)[0] + ".srt"
        if os.path.exists(srt):
            shutil.copy(srt, os.path.join(dst, "recording.srt"))
        notes = annotate_lines(os.path.join(os.path.dirname(src), "log.txt"))
        with open(os.path.join(dst, "annotate.txt"), "w", encoding="utf-8") as f:
            f.write("\n".join(notes) + ("\n" if notes else ""))
        try:
            if not make_gif(src, os.path.join(dst, "preview.gif")):
                print(f"{platform}: preview over {GIF_LIMIT} bytes even at the smallest size; video only")
        except subprocess.CalledProcessError as e:
            print(f"{platform}: no preview: {e}")
        found += 1
        print(f"{platform}: {src}")
    if not found:
        print("no annotated recordings in", args.artifacts)


def comment(args):
    raw = f"https://github.com/{args.repo}/raw/{args.sha}"
    head = args.head_sha[:7] if args.head_sha else ""
    lines = [MARKER, "### Annotated recordings", ""]
    lines.append("The bench journey recorded with `tb test --record --annotate`"
                 + (f" at {head}" if head else "") + f" ([workflow run]({args.run_url})). "
                 "Each step's caption is at the bottom; a ring shows each touch.")
    lines.append("")
    cells, notes = [], []
    for platform in PLATFORMS:
        d = os.path.join(args.out, platform)
        title = {"android": "Android", "ios": "iOS", "web": "Web"}[platform]
        if not os.path.exists(os.path.join(d, "recording.mp4")):
            cells.append(f"<td><b>{title}</b><br>no recording: see the {platform}-e2e job</td>")
            continue
        img = (f'<img src="{raw}/{platform}/preview.gif" width="260">'
               if os.path.exists(os.path.join(d, "preview.gif")) else "(no preview)")
        links = f'<a href="{raw}/{platform}/recording.mp4">full video (mp4)</a>'
        if os.path.exists(os.path.join(d, "recording.srt")):
            links += f' · <a href="{raw}/{platform}/recording.srt">captions</a>'
        cells.append(f"<td valign=\"top\"><b>{title}</b><br>{img}<br>{links}</td>")
        with open(os.path.join(d, "annotate.txt"), encoding="utf-8") as f:
            said = [l.strip() for l in f if l.strip()]
        sync = [s for s in said if "sync" in s or "behind" in s or "ahead of" in s or "no captions" in s]
        notes.append(f"- **{title}**: " + ("; ".join(sync) if sync else "no sync check logged"))
    lines.append("<table><tr>" + "".join(cells) + "</tr></table>")
    lines.append("")
    if notes:
        lines.append("Sync check (what `--annotate` found when it compared the touches with the video):")
        lines.append("")
        lines.extend(notes)
        lines.append("")
    with open(os.path.join(args.out, "comment.md"), "w", encoding="utf-8") as f:
        f.write("\n".join(lines))


def main():
    p = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    sub = p.add_subparsers(dest="cmd", required=True)
    pp = sub.add_parser("prepare")
    pp.add_argument("--artifacts", required=True)
    pp.add_argument("--out", required=True)
    pc = sub.add_parser("comment")
    pc.add_argument("--out", required=True)
    pc.add_argument("--repo", required=True)
    pc.add_argument("--sha", required=True)
    pc.add_argument("--run-url", required=True)
    pc.add_argument("--head-sha", default="")
    args = p.parse_args()
    {"prepare": prepare, "comment": comment}[args.cmd](args)


if __name__ == "__main__":
    sys.exit(main())
