#!/usr/bin/env python3
"""Generate the simulated IC approval call: assets/call/recording.mp4 plus the VTT and text
transcripts in the formats the Calls transcriber writes.

Needs macOS `say`, Pillow and ffmpeg (FFMPEG=/path/to/ffmpeg, or ffmpeg on PATH).
Avatars come from the preview dataset (AVATARS=<dataset>/assets/avatars).
"""
import os
import subprocess
import tempfile
import wave
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "assets" / "call"
AVATARS = Path(os.environ.get("AVATARS", Path.home() / "GitHub/mattermost-preview/datasets/meridian-soc-insider-threat/assets/avatars"))
FFMPEG = os.environ.get("FFMPEG", "ffmpeg")

PEOPLE = {
    "priya": ("Priya Raman", "Samantha", "priya.raman.png"),
    "marcus": ("Marcus Bell", "Daniel", "marcus.bell.png"),
}

SCRIPT = [
    ("priya", "Marcus, it's Priya on the SOC night shift. Sorry for the late call. I need a containment decision on an insider threat alert."),
    ("marcus", "No problem. Go ahead."),
    ("priya", "It's Elliot Marsh, the Halvorsen Systems contractor on AURORA. He resigned six days ago."),
    ("priya", "Tonight he badged into Building 7 at 22:41, signed in at 22:52, opened KESTREL documents he isn't on the roster for, and printed 340 pages at 23:40."),
    ("marcus", "KESTREL? He's never worked on that program. Is he still in the building?"),
    ("priya", "As far as we can tell, yes. Cohere North's investigation recommends quiet containment. Block printing, USB and file transfers on his workstation, preserve the evidence under legal hold, and add a 30 day watch rule."),
    ("priya", "No account or badge changes, so nothing tips him off."),
    ("marcus", "Agreed. Keep it quiet and reversible. Containment is approved."),
    ("marcus", "Brief counterintelligence before anything he could notice, and bring the decision group in first thing."),
    ("priya", "Will do. I'll set containment to approved and log the decision in the case."),
    ("marcus", "Thanks, Priya. Call me if he tries to leave with anything."),
]

RATE = 22050
GAP = 0.7
LEAD = 1.0
FPS = 4
W, H = 1280, 720


def synth(text, voice, path):
    subprocess.run(["say", "-v", voice, "-r", "185", "--file-format=WAVE",
                    f"--data-format=LEI16@{RATE}", "-o", str(path), text], check=True)
    with wave.open(str(path)) as w:
        return w.readframes(w.getnframes()), w.getnframes() / RATE


def ts(sec, ms=True):
    total = int(round(sec * 1000))
    h, rem = divmod(total, 3600000)
    m, rem = divmod(rem, 60000)
    s, milli = divmod(rem, 1000)
    if ms:
        return f"{h:02d}:{m:02d}:{s:02d}.{milli:03d}"
    return f"{h:02d}:{m:02d}:{int(round(rem / 1000)):02d}"


def circle(img, size):
    img = img.convert("RGBA").resize((size, size), Image.LANCZOS)
    mask = Image.new("L", (size, size), 0)
    ImageDraw.Draw(mask).ellipse((0, 0, size, size), fill=255)
    img.putalpha(mask)
    return img


def font(size, bold=False):
    for path in ["/System/Library/Fonts/HelveticaNeue.ttc", "/System/Library/Fonts/Helvetica.ttc"]:
        if Path(path).exists():
            return ImageFont.truetype(path, size, index=1 if bold else 0)
    return ImageFont.load_default()


def frame(t, speaker, avatars, total):
    img = Image.new("RGB", (W, H), (25, 27, 31))
    d = ImageDraw.Draw(img)
    d.text((32, 26), "DLP Response call", font=font(24, True), fill=(255, 255, 255))
    d.text((32, 58), f"{ts(t, False)[3:]}  ·  2 participants", font=font(18), fill=(160, 164, 172))

    tile_w, tile_h, gap = 520, 420, 40
    x0 = (W - 2 * tile_w - gap) // 2
    y0 = 150
    for i, key in enumerate(["priya", "marcus"]):
        x = x0 + i * (tile_w + gap)
        d.rounded_rectangle((x, y0, x + tile_w, y0 + tile_h), radius=12, fill=(37, 40, 46))
        cx, cy, r = x + tile_w // 2, y0 + 180, 90
        if key == speaker:
            d.ellipse((cx - r - 7, cy - r - 7, cx + r + 7, cy + r + 7), outline=(61, 184, 135), width=5)
        img.paste(avatars[key], (cx - r, cy - r), avatars[key])
        name = PEOPLE[key][0]
        f = font(24, True)
        tw = d.textlength(name, font=f)
        d.text((cx - tw / 2, cy + r + 30), name, font=f, fill=(255, 255, 255))
        status = "Speaking" if key == speaker else "Muted"
        f2 = font(17)
        sw = d.textlength(status, font=f2)
        d.text((cx - sw / 2, cy + r + 66), status, font=f2, fill=(61, 184, 135) if key == speaker else (140, 144, 152))
    return img


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    avatars = {k: circle(Image.open(AVATARS / v[2]), 180) for k, v in PEOPLE.items()}
    with tempfile.TemporaryDirectory() as tmp:
        tmp = Path(tmp)
        audio, segments, t = [b"\x00\x00" * int(LEAD * RATE)], [], LEAD
        for i, (who, text) in enumerate(SCRIPT):
            pcm, dur = synth(text, PEOPLE[who][1], tmp / f"s{i}.wav")
            segments.append((who, text, t, t + dur))
            audio += [pcm, b"\x00\x00" * int(GAP * RATE)]
            t += dur + GAP
        total = t + 0.5
        audio.append(b"\x00\x00" * int(0.5 * RATE))
        with wave.open(str(tmp / "audio.wav"), "wb") as w:
            w.setnchannels(1)
            w.setsampwidth(2)
            w.setframerate(RATE)
            w.writeframes(b"".join(audio))

        n = int(total * FPS) + 1
        for i in range(n):
            ft = i / FPS
            speaker = next((s[0] for s in segments if s[2] <= ft < s[3]), None)
            frame(ft, speaker, avatars, total).save(tmp / f"f{i:05d}.png")

        subprocess.run([FFMPEG, "-y", "-loglevel", "error", "-framerate", str(FPS), "-i", str(tmp / "f%05d.png"),
                        "-i", str(tmp / "audio.wav"), "-c:v", "libx264", "-tune", "stillimage", "-crf", "28",
                        "-pix_fmt", "yuv420p", "-r", "12", "-c:a", "aac", "-b:a", "64k", "-ac", "1",
                        "-shortest", "-movflags", "+faststart", str(OUT / "recording.mp4")], check=True)

    vtt = ["WEBVTT\n"]
    txt = []
    for who, text, start, end in segments:
        name = PEOPLE[who][0]
        vtt.append(f"\n{ts(start)} --> {ts(end)}\n<v {name}>({name}) {text}\n")
        txt.append(f"{ts(start, False)} -> {ts(end, False)}\n{name}\n{text}\n")
    (OUT / "transcript.vtt").write_text("".join(vtt))
    (OUT / "transcript.txt").write_text("\n".join(txt))
    print(f"{OUT / 'recording.mp4'}: {total:.1f}s, {(OUT / 'recording.mp4').stat().st_size // 1024} KB")


if __name__ == "__main__":
    main()
