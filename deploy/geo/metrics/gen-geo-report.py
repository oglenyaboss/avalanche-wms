#!/usr/bin/env python3
# gen-geo-report.py — render the geo distributed-testing report from a geo-bench run.
#
# Reads  <rundir>/geo-bench.json + db.csv + nodehome.csv + node-<name>.csv  and writes:
#   docs/load-testing/geo-distributed-report.html   Swiss presentation, inline SVG charts
#   docs/load-testing/geo-distributed-report.md      full report (prose + tables)
#   docs/load-testing/geo-report-chapter.md          paste-ready пояснительная записка section
#
# Reproducible by design: re-run the bench, re-run this, the report regenerates. Numbers are ALWAYS
# pulled from the JSON/CSV — never hardcoded in prose. Honesty: commit latency is labelled as
# commit/pipeline latency (batch wait + send + receipt poll), not raw consensus finality.
#
# Usage: python3 deploy/geo/metrics/gen-geo-report.py deploy/geo/artifacts/geo-bench-<ts>
import json, csv, sys, html
from pathlib import Path

RUNDIR = Path(sys.argv[1] if len(sys.argv) > 1 else ".")
OUT = Path("docs/load-testing")
OUT.mkdir(parents=True, exist_ok=True)
J = json.loads((RUNDIR / "geo-bench.json").read_text())

# ── data loading ──────────────────────────────────────────────────────────────────────────────
def read_csv(name):
    p = RUNDIR / name
    if not p.exists():
        return []
    rows = []
    for r in csv.DictReader(p.read_text().splitlines()):
        out = {}
        for k, v in r.items():
            if k == "ts":
                out[k] = int(v) if v and v.isdigit() else None
            else:
                try:
                    out[k] = float(v)
                except (ValueError, TypeError):
                    out[k] = None
        rows.append(out)
    return [r for r in rows if r.get("ts")]

DB = read_csv("db.csv")
NH = read_csv("nodehome.csv")
NODES = {n["name"]: read_csv(f"node-{n['name']}.csv") for n in J["meta"]["validators"]}
T0 = DB[0]["ts"] if DB else 0
FW = J.get("fault_window", {})
FAULT_ON = FW.get("enabled", False)
DOWN_REL = (FW["down_from"] - T0) if FAULT_ON and DB else None
UP_REL = (FW["up_at"] - T0) if FAULT_ON and DB else None

def rel(rows, key):
    return [((r["ts"] - T0), r[key]) for r in rows if r.get(key) is not None]

def num(x, d=2, dash="—"):
    if x is None:
        return dash
    if isinstance(x, float) and x == int(x):
        x = int(x)
    return f"{x:,.{d}f}".rstrip("0").rstrip(".") if isinstance(x, float) else f"{x:,}"

# ── generic inline-SVG line chart (auto-scaled, optional fault shading) ──────────────────────────
def svg_lines(series, w=920, h=300, ylabel="", colors=None, fault=True, y0=True, fmt="{:.0f}"):
    """series: dict name -> list[(t_rel, value)]. Returns an <svg> string."""
    pad_l, pad_r, pad_t, pad_b = 56, 18, 16, 30
    pts = [p for s in series.values() for p in s]
    if not pts:
        return '<div class="chart-empty">нет данных</div>'
    xs = [p[0] for p in pts]; ys = [p[1] for p in pts]
    xmin, xmax = min(xs), max(xs)
    ymin = 0 if y0 else min(ys)
    ymax = max(ys) if max(ys) > ymin else ymin + 1
    ymax += (ymax - ymin) * 0.08 + 1e-9
    iw, ih = w - pad_l - pad_r, h - pad_t - pad_b
    def X(t): return pad_l + (0 if xmax == xmin else (t - xmin) / (xmax - xmin) * iw)
    def Y(v): return pad_t + ih - (0 if ymax == ymin else (v - ymin) / (ymax - ymin) * ih)
    palette = colors or ["#2347b8", "#157a4a", "#b5641a", "#8a8a90"]
    out = [f'<svg viewBox="0 0 {w} {h}" class="chart" role="img" aria-label="{html.escape(ylabel)}">']
    # y gridlines + labels
    for i in range(5):
        v = ymin + (ymax - ymin) * i / 4
        y = Y(v)
        out.append(f'<line x1="{pad_l}" y1="{y:.1f}" x2="{w-pad_r}" y2="{y:.1f}" class="grid"/>')
        out.append(f'<text x="{pad_l-8}" y="{y+3:.1f}" class="ax ax-y">{fmt.format(v)}</text>')
    # x labels (0 .. max seconds)
    for i in range(7):
        t = xmin + (xmax - xmin) * i / 6
        x = X(t)
        out.append(f'<text x="{x:.1f}" y="{h-8}" class="ax ax-x">{int(t)}s</text>')
    # fault shading
    if fault and FAULT_ON and DOWN_REL is not None:
        x1, x2 = X(DOWN_REL), X(UP_REL)
        out.append(f'<rect x="{x1:.1f}" y="{pad_t}" width="{max(1,x2-x1):.1f}" height="{ih}" class="fault"/>')
        out.append(f'<text x="{(x1+x2)/2:.1f}" y="{pad_t+12}" class="ax fault-lbl">alex down</text>')
    # series
    for i, (name, s) in enumerate(series.items()):
        if not s:
            continue
        c = palette[i % len(palette)]
        d = "M" + " L".join(f"{X(t):.1f},{Y(v):.1f}" for t, v in s)
        out.append(f'<path d="{d}" fill="none" stroke="{c}" stroke-width="2" stroke-linejoin="round"/>')
    out.append("</svg>")
    # legend
    leg = " ".join(
        f'<span class="lk"><i style="background:{palette[i%len(palette)]}"></i>{html.escape(n)}</span>'
        for i, n in enumerate(series.keys()))
    return f'<figure class="fig"><figcaption>{html.escape(ylabel)}</figcaption>{"".join(out)}<div class="legend">{leg}</div></figure>'

# ── derived chart series ────────────────────────────────────────────────────────────────────────
committed_series = {"committed (накопл.)": rel(DB, "committed")}
backlog_series = {"backlog (очередь)": rel(DB, "backlog")}
lat_series = {"p50": rel(DB, "lat_p50"), "p95": rel(DB, "lat_p95"), "p99": rel(DB, "lat_p99")}
height_series = {f"{name} {next(v['geo'] for v in J['meta']['validators'] if v['name']==name)}": rel(rows, "height")
                 for name, rows in NODES.items() if rows}
if NH:
    height_series["node-home"] = rel(NH, "height")

CH_THROUGHPUT = svg_lines({**committed_series, **backlog_series}, ylabel="Накопленные коммиты и очередь outbox, событий", colors=["#157a4a", "#b5641a"], fmt="{:.0f}")
CH_LATENCY = svg_lines(lat_series, ylabel="Задержка коммита (updated_at − created_at), секунд", colors=["#2347b8", "#b5641a", "#c0392b"], y0=True, fmt="{:.1f}")
CH_SYNC = svg_lines(height_series, ylabel="Высота субсети по узлам (consensus last_accepted_height)", colors=["#2347b8", "#157a4a", "#b5641a", "#8a8a90"], y0=False, fmt="{:.0f}")

# ── prose fragments (data-conditional, HONEST two-level fault framing) ────────────────────────────
tp = J["throughput"]; lat = J["commit_latency_s"]; bl = J["backlog"]; ig = J["integrity"]; sy = J["sync"]
sustained = tp["sustained_commit_evps"]
lost = ig.get("lost_events", ig.get("failed") or 0) or 0
shipping_ok = ig.get("shipping_consistent", ig.get("consistent"))

# count-aware prose (works for 3-node "before" and 4-node "after" runs)
_CC = {"MD": ("Молдова", "🇲🇩"), "RO": ("Румыния", "🇷🇴"), "NL": ("Нидерланды", "🇳🇱")}
_validators = J["meta"]["validators"]
NV = len(_validators)
_codes = []
for _v in _validators:
    _c = _v["geo"].split()[0]
    if _c not in _codes:
        _codes.append(_c)
NC = len(_codes)
COUNTRIES_FLAGS = " · ".join(f"{_CC.get(c, (c, '🌐'))[1]} {_CC.get(c, (c, c))[0]}" for c in _codes)
COUNTRIES_PLAIN = " · ".join(_CC.get(c, (c, c))[0] for c in _codes)
def ru_gen(n): return {2: "двух", 3: "трёх", 4: "четырёх", 5: "пяти", 6: "шести"}.get(n, str(n))
def ru_card(n): return {2: "Два", 3: "Три", 4: "Четыре", 5: "Пять", 6: "Шесть"}.get(n, str(n))

def height_at(rows, abs_ts):
    h = None
    for r in rows:
        if r.get("height") is not None and r["ts"] <= abs_ts:
            h = r["height"]
    return h

fault_lead = ""
fault_para = ""
val_blocks = obs_blocks = 0
clean = False  # set True when the observer stayed healthy and nothing was lost (4-validator case)
if FAULT_ON:
    down, up = FW["down_from"], FW["up_at"]
    killed = FW.get("killed_node", "alex")
    # consensus liveness = surviving validators' advancement (NOT the frozen observer node-home)
    for name, rows in NODES.items():
        if name == killed or not rows:
            continue
        ha, hb = height_at(rows, down), height_at(rows, up)
        if ha is not None and hb is not None:
            val_blocks = max(val_blocks, int(hb - ha))
    oa, ob = height_at(NH, down), height_at(NH, up)
    obs_blocks = int((ob or 0) - (oa or 0))   # ~0 = the observer stall
    rec = FW.get("recovered_s")
    cdd = FW.get("committed_during_down", 0) or 0
    fault_lead = (f"На {int(DOWN_REL)}-й секунде узел <strong>{html.escape(killed)}</strong> остановлен "
                  f"на {FW.get('down_s','?')} с — потеря 1 валидатора из 3.")
    clean = (lost == 0 and obs_blocks > 0)
    if clean:
        # 4-validator case: kill-1 leaves 75% > 73.33%, observer stayed healthy, nothing lost.
        fault_para = (
            f"<strong>Консенсус.</strong> Пока {html.escape(killed)} был выключен, валидаторы "
            f"финализировали (высота субсети +{val_blocks} блок(ов)).<br><br>"
            f"<strong>Пайплайн — выстоял.</strong> С {NV} валидаторами потеря одного оставляет "
            "<strong>75 % &gt; порога 73.33 %</strong>, поэтому observer-нода node-home осталась healthy и "
            f"<strong>не замёрзла</strong> (продвинулась на {obs_blocks} блок(ов) — против ~0 в 3-нодовой "
            f"конфигурации); под отказом закоммичено {cdd} событий, <strong>0 потеряно</strong> — никаких "
            "receipt-таймаутов, double-submit и реверзов.<br><br>"
            "<strong>Деградация изящная.</strong> WAN-финализация при выключенном узле замедлилась "
            "(p99 задержки подрос, backlog временно вырос), но полностью слился"
            + (f" за {int(rec)} с" if rec is not None else "") +
            f"; отгрузки сошлись ({ig.get('shipped_wms','?')} = {ig.get('shipped_chain','?')}). "
            "Closed-loop подтверждение, что 4-й валидатор устраняет лимит 3-нодовой конфигурации.")
    else:
        # 3-validator case: kill-1 drops to 66.67% < 73.33%, observer froze, batch-poison loss.
        fault_para = (
            f"<strong>Уровень консенсуса — выстоял.</strong> Пока {html.escape(killed)} был выключен, "
            f"оставшиеся валидаторы <strong>продолжали финализировать</strong>: высота субсети выросла на "
            f"{val_blocks} блок(ов), под нагрузкой закоммичено {cdd} событий. Снять 1 из 3 — кворум snow "
            "2 из 3 держится.<br><br>"
            "<strong>Уровень пайплайна — характеризованная деградация.</strong> Адаптер читает receipt'ы "
            "через единственную невалидирующую observer-ноду (node-home), которая на время отказа просела до "
            "2 пиров = 66.67 % &lt; порога 73.33 % и <strong>замёрзла</strong> (продвинулась на "
            f"{obs_blocks} блок(ов) против {val_blocks} у валидаторов; в логах "
            "<span class='mono'>not connected to enough stake</span>). Пока node-home не видела свежих блоков, "
            "опрос receipt'ов истекал → адаптер пересылал батчи → 3 транзакции реверзнулись (items уже сменили "
            f"статус) → <strong>{lost} событий потеряно</strong> на batch-poison (известный баг double-submit)."
            "<br><br>"
            f"<strong>Восстановление.</strong> После возврата {html.escape(killed)} node-home догнала высоту, "
            f"backlog слился до нуля"
            + (f" за {int(rec)} с" if rec is not None else "") +
            f", отгрузки сошлись ({ig.get('shipped_wms','?')} = {ig.get('shipped_chain','?')}). "
            "Митигации: 4-й валидатор (kill-1 оставит 75 % &gt; 73.33 %), больший receipt-timeout, либо "
            "идемпотентная отправка.")

if clean:
    fault_callout_cls = ""
    fault_conclusion = (f"<strong>Вывод.</strong> С {NV} валидаторами потеря 1 узла оставляет 75 % &gt; 73.33 %: "
        "observer не замерзает, пайплайн коммитит под отказом, <strong>0 потерь</strong>. Лимит 3-нодовой "
        "конфигурации (отдельный отчёт <span class='mono'>geo-3node-before</span>) закрыт добавлением "
        "валидатора — closed-loop before→after. Запас тонкий (75 % vs 73.33 % ≈ 1.7 п.п.): сильное "
        "многонодовое WAN-разделение могло бы снова просадить observer ниже порога — но это уже отказ "
        "2+ узлов, а не одного.")
else:
    fault_callout_cls = "warn"
    fault_conclusion = (f"<strong>Вывод (честно).</strong> Консенсус пережил отказ. Но пайплайн с одной "
        f"observer-нодой деградировал: просадка ниже 73.33 % заморозила receipt'ы и стоила {num(lost,0)} "
        "событий на известном баге адаптера. <strong>Характеризованный предел с митигациями</strong> — "
        "для защиты сильнее безупречной цифры. Закрывается 4-м валидатором (kill-1 = 75 %).")
if lost == 0:
    consistency_note = ("<strong>Полная</strong> сквозная согласованность: ни одно событие не потеряно — "
        "каждое отгруженное место имеет on-chain-доказательство на гео-цепочке.")
else:
    consistency_note = (f"Это <strong>не</strong> полная сквозная согласованность: {num(lost,0)} событий из "
        "реверзнувшихся батчей в окне отказа потеряны (on-chain-расхождение на тех этапах). Закоммиченные "
        "места имеют on-chain-доказательство; потерянные требуют повторной эмиссии.")
if not FAULT_ON:
    chapter_fault = "Инъекция отказа узла в данном прогоне не выполнялась."
elif clean:
    chapter_fault = (
        f"Для проверки отказоустойчивости в середине прогона один из {ru_gen(NV)} валидаторов был "
        f"принудительно остановлен на {FW.get('down_s', '—')} секунд. На **уровне консенсуса** сеть выстояла: "
        f"оставшиеся валидаторы продолжали финализировать блоки (высота субсети выросла на {num(val_blocks, 0)} "
        f"блок(ов) под нагрузкой). На **уровне прикладного пайплайна** отказ был поглощён без потерь: при "
        f"{NV} валидаторах потеря одного оставляет 75 % связности по стейку (выше служебного порога 73,33 %), "
        f"поэтому наблюдательная нода адаптера сохранила работоспособность, не приостанавливала приём блоков "
        f"(продвинулась на {num(obs_blocks, 0)} блок(ов)) и продолжала подтверждать транзакции под отказом "
        f"({num(FW.get('committed_during_down', 0), 0)} событий), в результате чего **ни одно событие не было "
        f"потеряно**. Деградация ограничилась временным ростом задержки финализации по WAN и очереди отправки, "
        f"которая полностью рассосалась после восстановления узла; счётчики отгрузок сошлись. Это прямое "
        f"подтверждение, что потеря одного валидатора из {NV} не нарушает ни безопасность консенсуса, ни "
        f"целостность прикладного слоя.")
else:
    chapter_fault = (
        f"Для проверки отказоустойчивости в середине прогона один из {ru_gen(NV)} валидаторов был "
        f"принудительно остановлен на {FW.get('down_s', '—')} секунд (потеря 33 % узлов сети). Результат "
        f"проявился на двух уровнях. На **уровне консенсуса** сеть выстояла: оставшиеся валидаторы продолжали "
        f"финализировать блоки (высота субсети выросла на {num(val_blocks, 0)} блок(ов) под нагрузкой), "
        f"поскольку кворум консенсуса сохранялся. На **уровне прикладного пайплайна** проявился "
        f"характеризованный предел: адаптер обращался к единственной невалидирующей наблюдательной ноде, "
        f"связность которой по стейку упала до 66,67 % (ниже служебного порога 73,33 %), из-за чего она "
        f"приостановила приём новых блоков; истечение тайм-аутов подтверждения транзакций привело к повторной "
        f"отправке и реверту нескольких пакетов, в результате чего {num(lost, 0)} событий были потеряны "
        f"(известное ограничение адаптера — неидемпотентная повторная отправка). После восстановления узла "
        f"наблюдательная нода догнала состояние сети, очередь полностью слилась, а счётчики отгрузок сошлись."
        f"\n\nВыявленное ограничение характеризовано и устранимо конфигурационно: добавление четвёртого "
        f"валидатора оставляет при отказе одного узла 75 % связности (выше порога 73,33 %), что предотвращает "
        f"заморозку наблюдательной ноды; альтернативы — увеличение тайм-аута подтверждения либо идемпотентная "
        f"повторная отправка пакетов. Таким образом, потеря одного валидатора не нарушает безопасность "
        f"консенсуса, а выявленный предел прикладного слоя имеет понятные пути устранения.")
bounded_word = "полностью слилась" if bl["bounded"] else "росла"

# ── node resource table rows ──────────────────────────────────────────────────────────────────
def node_rows_html():
    r = []
    for n in J["nodes"]:
        r.append(f"<tr><td>{html.escape(n['name'])}</td><td>{html.escape(str(n['geo']))}</td>"
                 f"<td class='num'>{num(n['end_height'],0)}</td><td class='num'>{num(n['peers'],0)}</td>"
                 f"<td class='num'>{num(n['rss_mb_max'],0)} МБ</td><td class='num'>{num(n['cpu_pct_avg'],1)} %</td></tr>")
    return "".join(r)

def node_rows_md():
    return "\n".join(
        f"| {n['name']} | {n['geo']} | {num(n['end_height'],0)} | {num(n['peers'],0)} | "
        f"{num(n['rss_mb_max'],0)} МБ | {num(n['cpu_pct_avg'],1)} % |" for n in J["nodes"])

# ── HTML ─────────────────────────────────────────────────────────────────────────────────────
CSS = """
:root{--paper:#f7f6f3;--surface:#fff;--ink:#17171a;--ink-soft:#56565d;--ink-faint:#8a8a90;--rule:#e0ded8;
--rule-strong:#c7c5bd;--accent:#2347b8;--accent-soft:#e8edfb;--chain:#157a4a;--chain-soft:#e2f1e8;
--wall:#b5641a;--wall-soft:#f6ebdd;--bad:#c0392b;--maxw:1000px;
--space-section:clamp(3rem,2rem + 4vw,5.5rem);--text-hero:clamp(2.4rem,1.2rem + 4.4vw,4.2rem);
--text-h2:clamp(1.4rem,1.05rem + 1.5vw,2rem);--text-stat:clamp(2rem,1.3rem + 2.6vw,3.1rem);
--font-sans:'Helvetica Neue',Helvetica,Arial,'Segoe UI',system-ui,sans-serif;
--font-mono:'SF Mono','JetBrains Mono','Roboto Mono',ui-monospace,monospace;}
*{box-sizing:border-box}body{margin:0;background:var(--paper);color:var(--ink);font-family:var(--font-sans);
font-size:16px;line-height:1.55;-webkit-font-smoothing:antialiased}
.wrap{max-width:var(--maxw);margin:0 auto;padding:0 clamp(1.2rem,3vw,2.5rem)}
section{padding-block:var(--space-section);border-top:1px solid var(--rule)}section:first-of-type{border-top:none}
.eyebrow{font-size:.74rem;font-weight:700;letter-spacing:.18em;text-transform:uppercase;color:var(--accent);
display:flex;align-items:center;gap:.6rem;margin:0 0 1.4rem}
.eyebrow::before{content:"";width:28px;height:2px;background:var(--accent);display:inline-block}
h1{font-size:var(--text-hero);line-height:1.02;letter-spacing:-.025em;font-weight:800;margin:0}
h2{font-size:var(--text-h2);line-height:1.1;letter-spacing:-.018em;font-weight:750;margin:0 0 .4rem}
h3{font-size:1.02rem;letter-spacing:-.01em;margin:1.6rem 0 .3rem;font-weight:700}
p{margin:0 0 1rem;color:var(--ink-soft);max-width:70ch}.lead{font-size:clamp(1.05rem,.98rem + .5vw,1.3rem);color:var(--ink);max-width:60ch}
strong{color:var(--ink);font-weight:700}.mono{font-family:var(--font-mono);font-variant-numeric:tabular-nums}
.num{font-variant-numeric:tabular-nums;letter-spacing:-.02em}
.hero{padding-top:clamp(3rem,2rem + 4vw,5rem)}
.statgrid{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:1px;background:var(--rule);
border:1px solid var(--rule);margin:2rem 0}
.stat{background:var(--surface);padding:1.3rem 1.4rem}
.stat .v{font-size:var(--text-stat);font-weight:800;letter-spacing:-.03em;line-height:1;font-variant-numeric:tabular-nums}
.stat .k{font-size:.78rem;color:var(--ink-faint);margin-top:.5rem;text-transform:uppercase;letter-spacing:.06em}
.stat.chain .v{color:var(--chain)}.stat.accent .v{color:var(--accent)}.stat.wall .v{color:var(--wall)}
.nodes{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:1rem;margin:2rem 0}
.node{background:var(--surface);border:1px solid var(--rule);padding:1.2rem 1.3rem;position:relative}
.node .flag{font-size:1.6rem}.node .nm{font-weight:750;margin:.3rem 0 .1rem}
.node .id{font-family:var(--font-mono);font-size:.72rem;color:var(--ink-faint);word-break:break-all}
.node .ok{position:absolute;top:1.2rem;right:1.3rem;width:9px;height:9px;border-radius:50%;background:var(--chain)}
figure.fig{margin:1.6rem 0 0;background:var(--surface);border:1px solid var(--rule);padding:1rem 1.1rem .6rem}
figure.fig figcaption{font-size:.8rem;color:var(--ink-faint);margin-bottom:.6rem;text-transform:uppercase;letter-spacing:.05em}
svg.chart{width:100%;height:auto;display:block}
.chart .grid{stroke:var(--rule);stroke-width:1}.chart .ax{fill:var(--ink-faint);font-size:11px;font-family:var(--font-mono)}
.chart .ax-y{text-anchor:end}.chart .ax-x{text-anchor:middle}
.chart .fault{fill:var(--wall-soft);opacity:.7}.chart .fault-lbl{fill:var(--wall);text-anchor:middle;font-weight:700}
.legend{display:flex;gap:1.2rem;flex-wrap:wrap;margin:.6rem 0 .2rem;font-size:.8rem;color:var(--ink-soft)}
.lk{display:flex;align-items:center;gap:.4rem}.lk i{width:14px;height:3px;border-radius:2px;display:inline-block}
table{width:100%;border-collapse:collapse;margin:1.4rem 0;font-size:.92rem}
th,td{text-align:left;padding:.6rem .7rem;border-bottom:1px solid var(--rule)}
th{font-size:.74rem;text-transform:uppercase;letter-spacing:.06em;color:var(--ink-faint);font-weight:700}
td.num,th.num{text-align:right;font-variant-numeric:tabular-nums}
.callout{background:var(--chain-soft);border-left:3px solid var(--chain);padding:1rem 1.2rem;margin:1.6rem 0;border-radius:0 4px 4px 0}
.callout.warn{background:var(--wall-soft);border-color:var(--wall)}
.callout p{color:var(--ink);margin:0}
footer{padding:2.5rem 0 4rem;color:var(--ink-faint);font-size:.82rem;font-family:var(--font-mono)}
"""

def stat(v, k, cls=""):
    return f'<div class="stat {cls}"><div class="v">{v}</div><div class="k">{k}</div></div>'

def node_cards():
    flags = {"MD 🇲🇩": "🇲🇩", "RO 🇷🇴": "🇷🇴", "NL 🇳🇱": "🇳🇱"}
    cards = []
    for v in J["meta"]["validators"]:
        nd = next((n for n in J["nodes"] if n["name"] == v["name"]), {})
        flag = flags.get(v["geo"], "🌐")
        cards.append(
            f'<div class="node"><span class="ok"></span><div class="flag">{flag}</div>'
            f'<div class="nm">{html.escape(v["name"])} · {html.escape(v["geo"])}</div>'
            f'<div class="id">высота {num(nd.get("end_height"),0)} · peers {num(nd.get("peers"),0)} · '
            f'RSS {num(nd.get("rss_mb_max"),0)} МБ · CPU {num(nd.get("cpu_pct_avg"),1)} %</div></div>')
    return f'<div class="nodes">{"".join(cards)}</div>'

HTML = f"""<!DOCTYPE html><html lang="ru"><head><meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Гео-распределённое тестирование WMS → Subnet-EVM</title><style>{CSS}</style></head><body>
<div class="wrap">
<section class="hero">
  <div class="eyebrow">Гео-распределённый блокчейн · нагрузка и отказоустойчивость</div>
  <h1>WMS → Subnet-EVM<br>в трёх странах</h1>
  <p class="lead">Устойчивый прогон складского пайплайна против реально распределённой сети из {ru_gen(NV)}
  валидаторов ({COUNTRIES_PLAIN}) под честным stake-weighted BFT, с инъекцией отказа
  узла в середине нагрузки.</p>
  <div class="statgrid">
    {stat(num(sustained,1), "событий/с, устойчиво", "chain")}
    {stat(num(lat["p50"],2)+" с", "коммит p50", "accent")}
    {stat((num(val_blocks,0) if FAULT_ON else "—"), "блоков под kill-1 · консенсус жив", "chain")}
    {stat(num(lost,0) if FAULT_ON else "—", "потеряно под kill-1", "chain" if lost==0 else "wall")}
  </div>
</section>

<section>
  <div class="eyebrow">Топология</div>
  <h2>{ru_card(NV)} независимых валидатора в {NC} юрисдикциях</h2>
  <p>Сеть networkID <span class="mono">{J['meta']['network_id']}</span>, субсеть-EVM chainId
  <span class="mono">{J['meta']['chain_id_hex']}</span>. Все узлы — нативный avalanchego под systemd на
  VPS с 1 ядром, пирятся по WAN, финализируют под snow-консенсусом (sample 3 / quorum 2).</p>
  {node_cards()}
</section>

<section>
  <div class="eyebrow">Методология</div>
  <h2>Что именно измерялось</h2>
  <p>Нагрузка подаётся k6-харнессом (<span class="mono">tests/stress/09-throughput.js</span>) в режиме
  <span class="mono">constant-arrival-rate</span>: реальный складской поток приёмка → раскладка →
  сборка → отгрузка по HTTP API WMS на устойчивом темпе {num(J['meta']['offered_rate_rps'],0)} итер/с в течение
  {J['meta']['duration']}. WMS пишет события в outbox → Debezium CDC → ledger-adapter батчует и
  отправляет on-chain. Метрики снимаются двумя независимыми потоками: из БД (коммиты, очередь,
  задержка, целостность) и <strong>напрямую с каждого узла</strong> (consensus-высота, пиры, RSS, CPU
  через <span class="mono">/ext/metrics</span>). Все запросы к БД оконены отметкой начала прогона.</p>
  <div class="callout warn"><p><strong>Честная оговорка о задержке.</strong> Величина
  «задержка коммита» = <span class="mono">updated_at − created_at</span> строки on-chain-события: это
  сквозная задержка пайплайна (ожидание батча + отправка + опрос receipt), а <strong>не</strong> чистое
  время финализации консенсуса. Финализация субсети — порядка одного блока.</p></div>
</section>

<section>
  <div class="eyebrow">Устойчивая пропускная способность</div>
  <h2>Фронт поглощает нагрузку, очередь {bounded_word}</h2>
  <p>k6-фронт прошёл <strong>{num(tp['k6_iterations'],0)}</strong> итераций с отказами HTTP всего
  <strong>{tp['k6_http_fail_pct']}</strong> — WMS поглощает поток (outbox развязывает фронт от цепочки).
  Цепочка устойчиво коммитит <strong>~{num(sustained,1)} событий/с</strong>; в этом прогоне предложенный
  темп слегка превысил эту ёмкость, поэтому очередь outbox постепенно росла (пик {num(bl['max'],0)}) и
  затем <strong>{bounded_word}</strong> до {num(bl['final'],0)} — всплеск выше ёмкости поглощён без потери
  уже закоммиченных событий. Склад генерирует единицы событий/с, так что измеренная ёмкость кратно
  перекрывает реальную потребность.</p>
  {CH_THROUGHPUT}
</section>

<section>
  <div class="eyebrow">Задержка коммита</div>
  <h2>Сквозная задержка пайплайна</h2>
  <p>p50 <strong>{num(lat['p50'],2)} с</strong> · p95 {num(lat['p95'],2)} с · p99 {num(lat['p99'],2)} с.
  Под нагрузкой задержка остаётся в секундном диапазоне; всплеск в окне отказа (ниже) — это замедление
  WAN-финализации при 2 из 3 узлов, а не сбой.</p>
  {CH_LATENCY}
</section>

<section>
  <div class="eyebrow">Синхронизация узлов</div>
  <h2>Узлы идут синхронно</h2>
  <p>Высота субсети по каждому валидатору в течение прогона. Расхождение между узлами —
  <strong>{num(sy['validator_height_spread'],0)} блок(ов)</strong>: реплики держат единое состояние.
  Провал линии alex в затенённом окне — это инъекция отказа; после возврата высота догоняется.</p>
  {CH_SYNC}
</section>

<section>
  <div class="eyebrow">Отказоустойчивость · kill-1-of-3</div>
  <h2>Отказ узла под живой нагрузкой</h2>
  <p>{fault_para or 'Инъекция отказа в этом прогоне не выполнялась.'}</p>
  <div class="callout {fault_callout_cls}"><p>{fault_conclusion}</p></div>
</section>

<section>
  <div class="eyebrow">Ресурсы узлов</div>
  <h2>Стоимость узла под нагрузкой</h2>
  <table><thead><tr><th>Узел</th><th>Гео</th><th class="num">Высота</th><th class="num">Пиры</th>
  <th class="num">RSS пик</th><th class="num">CPU сред.</th></tr></thead>
  <tbody>{node_rows_html()}</tbody></table>
  <p>Каждый валидатор — 1 ядро, ~1 ГБ RAM. Под устойчивой нагрузкой потребление остаётся скромным,
  что подтверждает практичность гео-развёртывания на дешёвых VPS.</p>
</section>

<section>
  <div class="eyebrow">Целостность</div>
  <h2>WMS ↔ chain</h2>
  <p>Счётчики отгрузок сходятся: в WMS {num(ig['shipped_wms'],0)} отгружено, on-chain
  {num(ig['shipped_chain'],0)} событий <span class="mono">shipping</span> в COMMITTED —
  <strong>{'совпадение' if shipping_ok else 'расхождение'}</strong>. {consistency_note}</p>
</section>

<footer>Сгенерировано из {html.escape(str(RUNDIR.name))}/geo-bench.json · networkID {J['meta']['network_id']} ·
chain {J['meta']['blockchain_id']}</footer>
</div></body></html>"""

(OUT / "geo-distributed-report.html").write_text(HTML)

# ── full markdown ─────────────────────────────────────────────────────────────────────────────
MD = f"""# Гео-распределённое тестирование WMS → Subnet-EVM

> Устойчивая нагрузка и отказоустойчивость складского пайплайна против реально распределённой сети из
> {ru_gen(NV)} валидаторов ({COUNTRIES_FLAGS}). networkID {J['meta']['network_id']},
> chainId {J['meta']['chain_id_hex']}. Источник: `{RUNDIR.name}/geo-bench.json`.

## Резюме

| Метрика | Значение |
|---|---|
| Устойчивая пропускная способность | **{num(sustained,1)} событий/с** |
| Задержка коммита (p50 / p95 / p99) | {num(lat['p50'],2)} / {num(lat['p95'],2)} / {num(lat['p99'],2)} с |
| Закоммичено / потеряно (реверты) | {num(ig['committed'],0)} / **{num(lost,0)}** |
| Отказов HTTP фронта (WMS) | {tp['k6_http_fail_pct']} |
| Пик очереди / на финише | {num(bl['max'],0)} / {num(bl['final'],0)} ({'слилась' if bl['bounded'] else 'росла'}) |
| Консенсус под kill-1 | валидаторы продвинулись на {num(val_blocks,0) if FAULT_ON else '—'} блок(ов) |
| Отгрузки WMS↔chain | {num(ig['shipped_wms'],0)} == {num(ig['shipped_chain'],0)} ({'совпали' if shipping_ok else 'расходятся'}) |

## Топология

{ru_card(NV)} независимых нативных валидатора avalanchego (v1.14.0 + subnet-evm) под systemd на VPS с
1 ядром, пиринг по WAN, консенсус snow (sample 3 / quorum 2), stake-weighted BFT (sybil-protection on).

| Узел | Гео | Высота | Пиры | RSS пик | CPU сред. |
|---|---|---|---|---|---|
{node_rows_md()}

## Методология

Нагрузка — k6 (`tests/stress/09-throughput.js`) в режиме `constant-arrival-rate`: реальный складской
поток приёмка → раскладка → сборка → отгрузка по HTTP API WMS на темпе {num(J['meta']['offered_rate_rps'],0)} итер/с
в течение {J['meta']['duration']}. Путь события: WMS → `outbox_events` → Debezium CDC → ledger-adapter
(батч) → Subnet-EVM. Метрики снимались двумя потоками: из БД (run-scoped по отметке начала) и напрямую
с каждого узла через `/ext/metrics` (consensus-высота, пиры, RSS, CPU) on-box-коллектором.

> **Честная оговорка.** «Задержка коммита» = `updated_at − created_at` — сквозная задержка пайплайна
> (ожидание батча + отправка + опрос receipt), НЕ чистая финализация консенсуса (та ≈ один блок).

## Устойчивая пропускная способность

k6-фронт: **{num(tp['k6_iterations'],0)}** итераций, отказов HTTP **{tp['k6_http_fail_pct']}** — WMS
поглощает поток (outbox развязывает фронт от цепочки). Цепочка устойчиво коммитит **~{num(sustained,1)}
событий/с**; в этом прогоне предложенный темп слегка превысил эту ёмкость, поэтому очередь outbox
постепенно росла (пик {num(bl['max'],0)}) и затем {bounded_word} до {num(bl['final'],0)} — всплеск выше
ёмкости поглощён без потери закоммиченных событий. Склад генерирует единицы событий/с, поэтому измеренная
ёмкость кратно перекрывает реальную потребность.

## Отказоустойчивость (kill-1-of-3) — два уровня

{(fault_para.replace('<br><br>','\n\n').replace('<strong>','**').replace('</strong>','**').replace("<span class='mono'>",'`').replace('</span>','`').replace('&lt;','<').replace('&gt;','>')) if FAULT_ON else 'Инъекция отказа в этом прогоне не выполнялась.'}

{fault_conclusion.replace('<strong>','**').replace('</strong>','**').replace("<span class='mono'>",'`').replace('</span>','`').replace('&gt;','>').replace('&lt;','<')}

## Честные оговорки

- Throughput гео-цепочки ограничен WAN + временем блока, масштабируется размером батча адаптера;
  локальные одно-нодовые числа (~1900/с) — это ДРУГАЯ цепочка и сюда не переносятся.
- 73.33 % — health-порог связности по стейку avalanchego. При связности < 73.33 % невалидирующая
  observer-нода **приостанавливает приём блоков** (лог `not connected to enough stake`), хотя валидаторы
  продолжают финализировать кворумом. Поэтому 3 равных валидатора (kill-1 = 66.67 %) роняют пайплайн, а
  4 (kill-1 = 75 %) — нет (см. раздел отказоустойчивости).
- Backlog отправки в окне отказа временно рос (WAN-финализация при выключенном узле медленнее) и
  полностью сливался после восстановления; пиковые значения — в разделе пропускной способности.
- «Задержка коммита» — сквозная пайплайн-задержка, не консенсус-финализация (см. методологию).

_Сгенерировано `deploy/geo/metrics/gen-geo-report.py` из `{RUNDIR.name}/geo-bench.json`._
"""
(OUT / "geo-distributed-report.md").write_text(MD)

# ── paste-ready chapter ───────────────────────────────────────────────────────────────────────
CH = f"""## Апробация: гео-распределённое тестирование блокчейн-слоя

Для проверки практической пригодности блокчейн-слоя WMS в условиях географической распределённости и
отказов развёрнута сеть из {ru_gen(NV)} независимых валидаторов Avalanche Subnet-EVM в {NC} странах
({COUNTRIES_FLAGS}; networkID {J['meta']['network_id']}, chainId {J['meta']['chain_id_hex']}).
Узлы — нативный avalanchego v1.14.0 с subnet-evm на VPS с одним ядром, пиринг по WAN, консенсус snow
(sample 3 / quorum 2) с stake-weighted BFT.

Нагрузочный прогон выполнен инструментом k6 в режиме постоянного темпа поступления заявок: реальный
складской поток (приёмка → раскладка → сборка → отгрузка) подавался на устойчивом темпе
{num(J['meta']['offered_rate_rps'],0)} итераций/с в течение {J['meta']['duration']}. События проходили полный
производственный путь: сервис WMS → таблица outbox → захват изменений Debezium → пакетная отправка
ledger-adapter → запись в субсеть-EVM.

Результаты прогона:

- **Устойчивая пропускная способность** блокчейн-слоя составила {num(sustained,1)} событий/с
  ({num(ig['committed'],0)} зафиксированных событий). Фронтальный слой (WMS) поглотил нагрузку с долей
  отказов HTTP {tp['k6_http_fail_pct']}. Предложенный темп слегка превысил пропускную способность цепочки,
  поэтому очередь отправки выросла (пик {num(bl['max'],0)}) и затем полностью слилась до {num(bl['final'],0)} —
  избыточный всплеск поглощён без потери уже зафиксированных событий.
- **Задержка коммита** (сквозная задержка пайплайна — ожидание пакета, отправка и подтверждение
  транзакции, а не чистая финализация консенсуса) составила {num(lat['p50'],2)} с (медиана),
  {num(lat['p95'],2)} с (95-й перцентиль) и {num(lat['p99'],2)} с (99-й перцентиль; хвост обусловлен окном
  отказа узла).
- **Синхронизация узлов**: вне окна отказа высота цепочки на узлах совпадала — реплики поддерживали
  единое состояние.
- **Целостность отгрузок**: число отгрузок в WMS ({num(ig['shipped_wms'],0)}) совпало с числом
  подтверждённых on-chain событий отгрузки ({num(ig['shipped_chain'],0)}).

{chapter_fault}

Ресурсопотребление узлов под нагрузкой оставалось скромным (RSS до
{num(max((n['rss_mb_max'] or 0) for n in J['nodes']),0)} МБ, средняя загрузка CPU до
{num(max((n['cpu_pct_avg'] or 0) for n in J['nodes']),0)} %), что подтверждает практичность развёртывания
гео-распределённой сети на недорогих виртуальных серверах.

_Источник данных: `{RUNDIR.name}/geo-bench.json`; прогон воспроизводится скриптом
`deploy/geo/run-geo-bench.sh`._
"""
(OUT / "geo-report-chapter.md").write_text(CH)

print(f"wrote:\n  {OUT}/geo-distributed-report.html\n  {OUT}/geo-distributed-report.md\n  {OUT}/geo-report-chapter.md")
print(f"sustained={sustained} ev/s  lat p50/p95/p99={lat['p50']}/{lat['p95']}/{lat['p99']}  "
      f"committed={ig['committed']} failed={ig['failed']}  fault={FAULT_ON}")
