#!/usr/bin/env python3
"""Render the README animation. Optional dependency: Pillow.

Run: python3 scripts/render_animation.py
Pass --font /path/to/font.ttf when no supported system font is available.
This script uses recorded data only; it never calls a model or external service.
"""
from __future__ import annotations

import argparse
from pathlib import Path
from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parents[1]
WIDTH, HEIGHT = 960, 770
BG, FG, MUTED, TRACK = "#101820", "#f2f6fa", "#bdcad6", "#23313e"
GUARD, GENERATION, REFERENCE = "#5dd6bd", "#76aefa", "#ceacf2"
MEAN_JEV = 0.7970325


def find_font(value: str | None) -> str:
    if value:
        return value
    for path in (
        "/System/Library/Fonts/Supplemental/Arial.ttf",
        "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
        "C:/Windows/Fonts/arial.ttf",
    ):
        if Path(path).is_file():
            return path
    raise SystemExit("Provide a font with --font /path/to/font.ttf")


def render(t: float, font_path: str) -> Image.Image:
    image = Image.new("RGB", (WIDTH, HEIGHT), BG)
    d = ImageDraw.Draw(image)
    fonts = {size: ImageFont.truetype(font_path, size) for size in (15, 17, 19, 22, 30)}

    def text(x: int, y: int, label: str, size: int = 19, color: str = FG):
        d.text((x, y), label, fill=color, font=fonts[size])

    def right(y: int, label: str):
        width = d.textlength(label, font=fonts[19])
        text(int(WIDTH - 38 - width), y, label)

    def fmt(n: float) -> str:
        return f"{n:.3f}".replace(".", ",")

    def lane(y: int, label: str, segments: list[tuple[float, str]], maximum: float):
        left, end, bar_y = 38, WIDTH - 38, y + 29
        total = sum(duration for duration, _ in segments)
        text(left, y, label)
        right(y, fmt(total) + " sn")
        d.rounded_rectangle((left, bar_y, end, bar_y + 13), radius=3, fill=TRACK)
        offset = 0.0
        for duration, color in segments:
            shown = max(0.0, min(duration, t - offset))
            if shown > 0:
                start_x = left + (end - left) * offset / maximum
                stop_x = left + (end - left) * (offset + shown) / maximum
                d.rectangle((start_x, bar_y, stop_x, bar_y + 13), fill=color)
            offset += duration

    text(38, 24, "Guardrail ne kadar bekletiyor?", 30)
    text(38, 66, "19 Eylül 2026 • Süre karşılaştırması; güvenlik kalitesi ölçümü değil", 17, MUTED)
    text(38, 112, "1. Aynı deneyde tek karar", 22)
    text(38, 144, "WotAI • 150 metin • p50 • Üslup sınıflandırması, saldırı tespiti değil", 17, MUTED)
    lane(185, "Jev", [(0.455, GUARD)], 1.8)
    lane(242, "Claude Haiku 4.5", [(0.631, REFERENCE)], 1.8)
    lane(299, "Claude Sonnet 5", [(1.674, REFERENCE)], 1.8)
    text(38, 354, "Jev: Haiku'ya göre %28, Sonnet'e göre %73 daha kısa karar süresi.", 17, MUTED)
    d.line((38, 392, WIDTH - 38, 392), fill=TRACK)
    text(38, 415, "2. Bizim proxy • Örnek senaryo", 22)
    text(38, 449, "Tam cevap: 3 sn varsayım • Giriş + çıkış kontrolü • Önbellek yok", 17, MUTED)
    lane(486, "Kontrolsüz referans", [(3.0, GENERATION)], 6.4)
    lane(543, "Jev • 797 ms/kontrol tahmini", [(MEAN_JEV, GUARD), (3.0, GENERATION), (MEAN_JEV, GUARD)], 6.4)
    lane(600, "LLM hakem • 1.500 ms/kontrol varsayımı", [(1.5, GUARD), (3.0, GENERATION), (1.5, GUARD)], 6.4)
    text(38, 659, "Jev: 4 canlı çağrının ortalaması • İki aşamalı proxy süresi ölçülmüş değildir.", 17, MUTED)
    text(38, 689, "Bu senaryoda kazanç: 1,406 sn (%23,4) • Grafikler farklı zaman ölçeğinde", 17)
    d.rectangle((38, 733, 50, 745), fill=GUARD)
    text(58, 729, "Kontrol", 15, MUTED)
    d.rectangle((150, 733, 162, 745), fill=GENERATION)
    text(170, 729, "Cevap üretimi", 15, MUTED)
    text(535, 729, "Kaynaklar: docs/latency-research-2026-09-19.md", 15, MUTED)
    return image


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--font")
    parser.add_argument("--output", type=Path, default=ROOT / "docs/assets/guardrail-latency.gif")
    args = parser.parse_args()
    font = find_font(args.font)
    times = [6.1] + [i / 12 for i in range(73)]
    frames = [render(t, font).quantize(colors=64) for t in times]
    durations = [1200] + [80] * 72 + [2200]
    args.output.parent.mkdir(parents=True, exist_ok=True)
    frames[0].save(args.output, save_all=True, append_images=frames[1:],
                   duration=durations, loop=0, optimize=True, disposal=2)
    print(args.output)


if __name__ == "__main__":
    main()
