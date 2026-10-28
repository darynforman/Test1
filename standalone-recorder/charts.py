#!/usr/bin/env python3
"""Export the two assessment charts from runs.csv; never write measurement files.

Example (using the plotting library already installed temporarily for these charts):
  PYTHONPATH=/tmp/imagelab-chart-libs MPLCONFIGDIR=/tmp/imagelab-chart-cache \
    python3 standalone-recorder/charts.py
"""
import argparse
import csv
from dataclasses import dataclass
import hashlib
import json
from pathlib import Path
import statistics

import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
from matplotlib.patches import Patch
from matplotlib.ticker import MultipleLocator
from matplotlib.transforms import Bbox

CONDITIONS = ('A', 'B', 'C1', 'C2', 'C3')
MODES = ('short', 'long')
COLORS = {'short': '#CF167E', 'long': '#008C91'}
LABELS = ('A\nQuiet job\n30 s delay', 'B\nReal image\nprocessing',
          'C1\nSimulated updates\nevery 2 s', 'C2\nSimulated updates\nevery 200 ms',
          'C3\n200 ms updates\n+ added latency')
DPI = 300


@dataclass(frozen=True)
class Chart:
    metric: str
    filename: str
    title: str
    axis: str
    definition: str
    tick_step: int


CHARTS = (
    Chart('requests_started', 'request-count-chart.png',
          'Observation requests by condition', 'Observation requests (GETs per run)',
          'Counts include job-status GETs only; upload POST requests are excluded.', 10),
    Chart('detection_ms', 'detection-delay-chart.png',
          'Completion-detection delay by condition', 'Completion-detection delay (ms)',
          'Delay = browser JSON receipt time − server post-commit log time; same-computer estimate.', 200),
)


def load_measurements(root):
    with (root / 'runs.csv').open(newline='') as handle:
        rows = list(csv.DictReader(handle))
    expected = {f'{c}-{m}-{rep}' for c in CONDITIONS for m in MODES for rep in (1, 2, 3)}
    if len(rows) != 30 or {r['run_id'] for r in rows} != expected:
        raise ValueError('Expected exactly the 30 unique measured runs. Nothing was plotted.')
    grouped = {(c, m): [r for r in rows if r['condition'] == c and r['mode'] == m]
               for c in CONDITIONS for m in MODES}
    for key, group in grouped.items():
        if len(group) != 3:
            raise ValueError(f'Expected three runs for {key}')
    actual_latency = []
    for mode in MODES:
        for repetition in (1, 2, 3):
            timing = json.loads((root / f'C3-{mode}-{repetition}-prompt-get.json').read_text())
            before = statistics.median(r['round_trip_ms'] for r in timing['baseline_readings'])
            after = statistics.median(r['round_trip_ms'] for r in timing['readings'])
            if after - before != timing['measured_additional_median_ms']:
                raise ValueError('Latency timing record does not agree with its underlying readings.')
            if timing['configured']['latency'] != 200:
                raise ValueError('Unexpected configured C3 latency.')
            actual_latency.append(after - before)
    return grouped, actual_latency


def check_layout(fig, ax, texts, value_labels, marker_boxes):
    """Check rendered text bounds and labels against the actual point/range bounds."""
    fig.canvas.draw()
    renderer = fig.canvas.get_renderer()
    canvas = fig.bbox
    all_texts = texts + value_labels + ax.get_xticklabels() + ax.get_yticklabels() + [ax.yaxis.label]
    text_boxes = [(text, text.get_window_extent(renderer)) for text in all_texts
                  if text.get_visible() and text.get_text()]
    for text, box in text_boxes:
        if not (box.x0 >= canvas.x0 and box.y0 >= canvas.y0 and
                box.x1 <= canvas.x1 and box.y1 <= canvas.y1):
            raise ValueError('Clipped text: ' + text.get_text())
    # The intentionally displaced run dots are checked using their real display radius.
    radius = (54 ** .5 / 2 + 1.9) * fig.dpi / 72
    obstacles = []
    for x, minimum, maximum, point_positions in marker_boxes:
        low = ax.transData.transform((x, minimum))
        high = ax.transData.transform((x, maximum))
        cap = 6 * fig.dpi / 72
        obstacles.append(Bbox.from_extents(low[0] - cap, low[1] - 2,
                                          high[0] + cap, high[1] + 2))
        for px, py in point_positions:
            cx, cy = ax.transData.transform((px, py))
            obstacles.append(Bbox.from_extents(cx-radius, cy-radius, cx+radius, cy+radius))
    for box in obstacles:
        if not (box.x0 >= canvas.x0 and box.y0 >= canvas.y0 and box.x1 <= canvas.x1 and box.y1 <= canvas.y1):
            raise ValueError('A run dot or range marker is clipped at the figure edge')
    value_boxes = [text.get_window_extent(renderer) for text in value_labels]
    for i, box in enumerate(value_boxes):
        if any(box.overlaps(other) for other in value_boxes[i+1:]):
            raise ValueError('Overlapping value labels')
        if any(box.overlaps(obstacle) for obstacle in obstacles):
            raise ValueError('A median label collides with a run dot or range line')
        for text, other in text_boxes:
            if text not in value_labels and box.overlaps(other):
                raise ValueError('A value label collides with other chart text')
    # Verify every other independently positioned text block as well.
    for i, (text, box) in enumerate(text_boxes):
        for other_text, other_box in text_boxes[i+1:]:
            if box.overlaps(other_box):
                raise ValueError(f'Text overlap: {text.get_text()!r} / {other_text.get_text()!r}')


def export_chart(root, chart, grouped, actual_latency):
    plt.rcParams.update({
        'font.family': 'DejaVu Sans', 'font.size': 14,
        'figure.facecolor': 'white', 'axes.facecolor': 'white',
        'savefig.facecolor': 'white', 'axes.labelcolor': '#262335',
        'text.color': '#262335', 'xtick.color': '#3C3B49', 'ytick.color': '#595A68',
        'axes.spines.top': False, 'axes.spines.right': False, 'axes.spines.left': False,
        'axes.edgecolor': '#D8DCE5', 'axes.linewidth': 1,
    })
    fig, ax = plt.subplots(figsize=(13.2, 8.7), dpi=DPI)
    fig.subplots_adjust(left=.105, right=.98, bottom=.335, top=.805)
    ax.set_axisbelow(True)
    ax.grid(axis='y', color='#E9ECF2', linewidth=.9)
    maximum_value = max(int(r[chart.metric]) for group in grouped.values() for r in group)
    ax.set_ylim(0, maximum_value * 1.22)
    ax.set_xlim(-.58, 4.58)
    ax.yaxis.set_major_locator(MultipleLocator(chart.tick_step))
    ax.set_ylabel(chart.axis, fontsize=16, labelpad=17)
    ax.set_xticks(range(5), LABELS, fontsize=13.5, linespacing=1.35)
    ax.tick_params(axis='both', length=0, pad=11)
    ax.tick_params(axis='y', labelsize=13)
    value_labels, marker_boxes = [], []
    bar_checks = []
    for mode, offset in (('short', -.20), ('long', .20)):
        for index, condition in enumerate(CONDITIONS):
            group = sorted(grouped[condition, mode], key=lambda r: int(r['repetition']))
            values = [int(r[chart.metric]) for r in group]
            median, minimum, maximum = statistics.median(values), min(values), max(values)
            x = index + offset
            bar = ax.bar([x], [median], width=.31, color=COLORS[mode], linewidth=0, zorder=2)[0]
            ranges = ax.errorbar([x], [median],
                        yerr=[[median-minimum], [maximum-median]], fmt='none',
                        ecolor='#525A6A', elinewidth=1.6, capsize=6, capthick=1.6, zorder=3, clip_on=False)
            for artist in (*ranges.lines[1], *ranges.lines[2]):
                artist.set_clip_on(False)
            px = [x-.075, x, x+.075]
            points = ax.scatter(px, values, s=54, facecolors='white', edgecolors=COLORS[mode],
                       linewidths=1.9, zorder=4, clip_on=False)
            # The number is the MEDIAN. Put it above the highest run/range marker
            # so the full minimum–maximum line and all run dots remain visible.
            value_labels.append(ax.annotate(f'{median:g}', (x, maximum), xytext=(0, 13),
                                            textcoords='offset points', ha='center', va='bottom',
                                            fontsize=15.5, fontweight='bold', color=COLORS[mode],
                                            annotation_clip=False, zorder=5))
            marker_boxes.append((x, minimum, maximum, list(zip(px, values))))
            bar_checks.append((bar, points, ranges, median, minimum, maximum, condition, mode, values))
    texts = [
        fig.text(.105, .965, chart.title, ha='left', va='top', fontsize=25, fontweight='bold'),
        fig.text(.105, .905, 'Short vs long polling · 3 runs per mode in each condition',
                 ha='left', va='top', fontsize=14, color='#656473'),
        fig.text(.105, .179,
                 'Bars and number labels: median. Dots: individual runs. Range lines: minimum–maximum.',
                 ha='left', va='top', fontsize=12, color='#454655'),
        fig.text(.105, .129, chart.definition,
                 ha='left', va='top', fontsize=12, color='#454655'),
        fig.text(.105, .079,
                 f'C3: 200 ms configured latency; measured prompt-GET increases: {min(actual_latency):g}–{max(actual_latency):g} ms.',
                 ha='left', va='top', fontsize=12, color='#454655'),
        fig.text(.105, .031, 'Source: runs.csv · 30 measured runs · 5 conditions × 2 modes × 3 repetitions',
                 ha='left', va='bottom', fontsize=11.5, color='#777481'),
    ]
    legend = fig.legend(handles=[Patch(facecolor=COLORS[m], label=m.capitalize()+' polling') for m in MODES],
                        loc='lower left', bbox_to_anchor=(.097, .831), ncol=2,
                        frameon=False, fontsize=14, handlelength=1.5, columnspacing=2.4)
    texts.extend(legend.get_texts())
    for bar, points, ranges, median, minimum, maximum, condition, mode, values in bar_checks:
        if bar.get_height() != median or median != statistics.median(values):
            raise ValueError(f'Incorrect plotted height for {condition}/{mode}')
        if list(points.get_offsets()[:, 1]) != values:
            raise ValueError(f'Incorrect individual-run dots for {condition}/{mode}')
        segment = ranges.lines[2][0].get_segments()[0]
        if tuple(segment[:, 1]) != (minimum, maximum):
            raise ValueError(f'Incorrect minimum–maximum range for {condition}/{mode}')
        if matplotlib.colors.to_hex(bar.get_facecolor()).upper() != COLORS[mode]:
            raise ValueError('Bar color does not match the requested palette')
    check_layout(fig, ax, texts, value_labels, marker_boxes)
    target = root / chart.filename
    # Validate before replacing an existing export.
    temporary = target.with_suffix('.tmp.png')
    fig.savefig(temporary, dpi=DPI, facecolor='white', transparent=False,
                metadata={'Title':chart.title, 'Description':'Actual runs.csv data; medians, individual runs and minimum–maximum ranges.'})
    temporary.replace(target)
    plt.close(fig)
    print(f'Saved {target.name}: {13.2*DPI:g} × {8.7*DPI:g} px at {DPI} DPI; '
          '10 medians, 30 individual runs, and 10 ranges checked against runs.csv; layout checks passed.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--results-dir', type=Path,
                        default=Path(__file__).resolve().parent/'results/restart-20261025-101935')
    args = parser.parse_args()
    source = args.results_dir/'runs.csv'
    before = hashlib.sha256(source.read_bytes()).hexdigest()
    grouped, latency = load_measurements(args.results_dir)
    for chart in CHARTS:
        export_chart(args.results_dir, chart, grouped, latency)
    if hashlib.sha256(source.read_bytes()).hexdigest() != before:
        raise RuntimeError('Measurement CSV changed during export')
    print('Measurement CSV unchanged. No experiments or application code were executed.')


if __name__ == '__main__':
    main()
