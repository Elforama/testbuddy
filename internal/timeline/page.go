package timeline

// pageHTML is the timeline page. Screenshots are CSS classes (.shot-N) defined once in the
// head, so a screen shown as one step's "After" and the next step's "Before" is embedded
// once. A few lines of script add the enlarged view and the toolbar; the page reads fine
// without them.
const pageHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}} · tb timeline</title>
<style>
:root{--bg:#ffffff;--fg:#1d1d1f;--muted:#6e6e73;--card:#f6f8fa;--line:#d8dee4;--ok:#1a7f37;--bad:#cf222e;--warn:#9a6700;--skip:#8c959f;--accent:#0969da;--shotbg:#eaeef2}
@media (prefers-color-scheme:dark){:root{--bg:#0d1117;--fg:#e6edf3;--muted:#8d96a0;--card:#161b22;--line:#30363d;--ok:#3fb950;--bad:#f85149;--warn:#d29922;--skip:#6e7681;--accent:#4493f8;--shotbg:#21262d}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",system-ui,sans-serif}
.wrap{max-width:1120px;margin:0 auto;padding:24px 16px 64px}
header h1{font-size:22px;line-height:1.25;margin:0 0 6px;display:flex;gap:10px;align-items:center;flex-wrap:wrap}
.pill{font-size:12px;font-weight:600;padding:2px 10px;border-radius:999px;color:#fff;text-transform:uppercase;letter-spacing:.04em}
.pill.passed{background:var(--ok)}.pill.failed{background:var(--bad)}.pill.warned{background:var(--warn)}.pill.skipped,.pill.unfinished{background:var(--skip)}
.meta{color:var(--muted);display:flex;flex-wrap:wrap;gap:4px 14px;margin:0}
.meta b{color:var(--fg);font-weight:500}
.err{background:color-mix(in srgb,var(--bad) 10%,var(--bg));border:1px solid color-mix(in srgb,var(--bad) 40%,var(--bg));border-radius:8px;padding:10px 12px;margin:12px 0 0;white-space:pre-wrap;word-break:break-word;font:12.5px/1.45 ui-monospace,SFMono-Regular,Menlo,monospace}
.jump{display:inline-block;margin-top:10px;color:var(--bad);font-weight:600;text-decoration:none}
.jump:hover{text-decoration:underline}
.bar{display:flex;height:18px;margin:20px 0 6px;border-radius:6px;overflow:hidden;background:var(--line);gap:1px}
.bar a{display:block;min-width:3px;background:var(--ok);opacity:.85}
.bar a:hover{opacity:1;outline:2px solid var(--fg);outline-offset:-2px}
.bar a.failed{background:var(--bad)}.bar a.warned{background:var(--warn)}.bar a.skipped,.bar a.unfinished{background:var(--skip)}
.barlabel{display:flex;justify-content:space-between;color:var(--muted);font-size:12px;margin-bottom:16px}
.videos{display:flex;flex-wrap:wrap;gap:14px;margin:0 0 18px}
.videos figure{background:var(--card);border:1px solid var(--line);border-radius:8px;padding:8px}
.videos video{display:block;height:360px;max-width:100%;background:#000;border-radius:6px}
.seek{font:inherit;font-size:12px;color:var(--accent);background:none;border:none;padding:0;margin-left:8px;cursor:pointer}
.seek:hover{text-decoration:underline}
.tools{display:flex;gap:8px;flex-wrap:wrap;margin:0 0 14px}
.tools button{font:inherit;font-size:13px;color:var(--fg);background:var(--card);border:1px solid var(--line);border-radius:6px;padding:4px 10px;cursor:pointer}
.tools button[aria-pressed=true]{border-color:var(--accent);color:var(--accent)}
.step{border-left:3px solid var(--ok);background:var(--card);border-radius:0 8px 8px 0;margin:8px 0;padding:8px 12px;scroll-margin-top:16px}
.step.failed{border-left-color:var(--bad)}.step.warned{border-left-color:var(--warn)}.step.skipped,.step.unfinished{border-left-color:var(--skip)}
.step.group{background:transparent;padding:4px 12px;border-left-style:dashed}
.step:target{box-shadow:0 0 0 2px var(--accent)}
.head{display:grid;grid-template-columns:22px 1fr auto;gap:8px;align-items:baseline}
.mark{font-weight:700;text-align:center}
.passed .mark{color:var(--ok)}.failed .mark{color:var(--bad)}.warned .mark{color:var(--warn)}.skipped .mark,.unfinished .mark{color:var(--skip)}
.desc{font-weight:500;word-break:break-word}
.group .desc{color:var(--muted);font-weight:600}
.where{color:var(--muted);font-size:12px;margin-left:6px;font-weight:400}
.role{font-size:11px;font-weight:600;color:var(--accent);border:1px solid var(--accent);border-radius:4px;padding:0 5px;margin-right:6px}
.time{color:var(--muted);font-size:12px;white-space:nowrap;font-variant-numeric:tabular-nums}
.step .err{margin:8px 0 0}
.frames{display:flex;flex-wrap:wrap;gap:14px;margin:10px 0 2px 30px}
figure{margin:0}
figcaption{font-size:12px;color:var(--muted);margin-bottom:4px}
.shot{position:relative;height:300px;background-color:var(--shotbg);background-size:contain;background-repeat:no-repeat;background-position:center;border:1px solid var(--line);border-radius:6px;cursor:zoom-in;padding:0;display:block}
.shot:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
.noshot{height:300px;width:150px;display:flex;align-items:center;justify-content:center;text-align:center;color:var(--muted);font-size:12px;border:1px dashed var(--line);border-radius:6px;padding:8px}
.arrow{align-self:center;color:var(--muted);font-size:18px}
.overlay{position:absolute;inset:0;width:100%;height:100%;pointer-events:none;overflow:visible;filter:drop-shadow(0 0 1.5px rgba(255,255,255,.95)) drop-shadow(0 0 1px rgba(0,0,0,.6))}
.g-ring{fill:none;stroke:#ff2d55}.g-dot,.g-head,.g-start{fill:#ff2d55}.g-fill{fill:rgba(255,45,85,.45)}.g-line{stroke:#ff2d55;stroke-linecap:round}
.g-num{fill:#ff2d55;font-weight:700;font-family:system-ui,sans-serif}
details.log{margin:8px 0 0 30px}
details.log summary{cursor:pointer;color:var(--muted);font-size:12px}
pre{margin:6px 0 0;padding:8px 10px;background:var(--bg);border:1px solid var(--line);border-radius:6px;overflow-x:auto;font:12px/1.45 ui-monospace,SFMono-Regular,Menlo,monospace;white-space:pre}
body.only-failed .step:not(.failed):not(.has-failed){display:none}
dialog{border:none;padding:0;background:transparent;max-width:96vw;max-height:96vh}
dialog::backdrop{background:rgba(0,0,0,.8)}
dialog .shot{height:88vh;cursor:zoom-out;border:none}
dialog p{color:#fff;text-align:center;margin:8px 0 0;font-size:13px}
@media (max-width:640px){.shot,.noshot{height:220px}.frames,details.log{margin-left:0}.head{grid-template-columns:18px 1fr}.time{grid-column:2}}
{{.Shots}}
</style></head>
<body><div class="wrap">
<header>
<h1><span class="pill {{.Status}}">{{.Status}}</span>{{.Title}}</h1>
<p class="meta">{{if .File}}<span><b>{{.File}}</b></span>{{end}}{{if .Devices}}<span>{{.Devices}}</span>{{end}}<span>{{.Duration}}</span>{{if .Started}}<span>{{.Started}}</span>{{end}}<span>{{.Counts}}</span>{{if .Version}}<span>tb {{.Version}}</span>{{end}}</p>
{{if .Error}}<div class="err">{{.Error}}</div>{{end}}
{{if .Failed}}<a class="jump" href="#s{{.Failed}}">Go to the failed step ↓</a>{{end}}
</header>
{{if .Segments}}<nav class="bar" aria-label="Steps by time">{{range .Segments}}<a class="{{.Status}}" href="#s{{.ID}}" style="width:{{.Pct}}" title="{{.Title}}"></a>{{end}}</nav>
<div class="barlabel"><span>start</span><span>{{.Duration}}</span></div>{{end}}
{{if .Videos}}<section class="videos" aria-label="Screen recording">{{range .Videos}}<figure><figcaption>{{.Label}}</figcaption><video id="video-{{.ID}}" src="{{.Src}}" controls preload="metadata"></video></figure>{{end}}</section>{{end}}
<div class="tools">
<button type="button" id="only-failed" aria-pressed="false">Failed steps only</button>
<button type="button" id="open-logs">Open all traces</button>
<button type="button" id="close-logs">Close all traces</button>
</div>
<main>
{{range .Rows}}<section class="step {{.Status}}{{if .Group}} group{{end}}" id="s{{.ID}}" style="margin-left:{{indent .Depth}}">
<div class="head"><span class="mark" aria-label="{{.Status}}">{{.Mark}}</span>
<span class="desc">{{if and $.Multi .Role}}<span class="role">{{.Role}}</span>{{end}}{{.Desc}}{{if .Where}}<span class="where">{{.Where}}</span>{{end}}</span>
<span class="time">{{.At}} · {{.Took}}{{if .Video}}<button type="button" class="seek" data-video="video-{{.Video}}" data-t="{{.Seek}}">▶ Video</button>{{end}}</span></div>
{{if .Err}}<div class="err">{{.Err}}</div>{{end}}
{{if .Frames}}<div class="frames">{{range $i, $f := .Frames}}{{if $i}}<span class="arrow" aria-hidden="true">→</span>{{end}}<figure><figcaption>{{$f.Label}}</figcaption>{{if $f.Shot}}<button type="button" class="shot shot-{{$f.Shot}}" style="aspect-ratio:{{$f.Ratio}}" aria-label="Enlarge: {{$f.Label}}">{{$f.Overlay}}</button>{{else}}<div class="noshot">{{$f.Note}}</div>{{end}}</figure>{{end}}</div>{{end}}
{{if .Lines}}<details class="log"{{if eq .Status "failed"}} open{{end}}><summary>Trace · {{len .Lines}} {{if eq (len .Lines) 1}}line{{else}}lines{{end}}</summary><pre>{{range .Lines}}{{.}}
{{end}}</pre></details>{{end}}
</section>
{{end}}
</main>
{{if .RunLog}}<details class="log" style="margin-left:0;margin-top:20px"><summary>Run log · {{len .RunLog}} lines</summary><pre>{{range .RunLog}}{{.}}
{{end}}</pre></details>{{end}}
</div>
<dialog id="zoom"><div id="zoom-body"></div><p>Click or press Esc to close</p></dialog>
<script>
(function(){
  var d=document.getElementById('zoom'),body=document.getElementById('zoom-body');
  document.addEventListener('click',function(e){
    var k=e.target.closest('.seek');
    if(k){var v=document.getElementById(k.dataset.video);if(v){v.currentTime=parseFloat(k.dataset.t);v.scrollIntoView({block:'nearest'});v.play()}return}
    var s=e.target.closest('main .shot');
    if(s&&d.showModal){var c=s.cloneNode(true);c.removeAttribute('aria-label');c.tabIndex=-1;body.replaceChildren(c);d.showModal();return}
    if(e.target.closest('dialog'))d.close();
  });
  // A failed step's groups stay visible with "failed steps only".
  document.querySelectorAll('.step.failed').forEach(function(s){
    var m=parseInt(s.style.marginLeft)||0,p=s.previousElementSibling;
    while(p){var pm=parseInt(p.style.marginLeft)||0;if(pm<m){p.classList.add('has-failed');m=pm}p=p.previousElementSibling}
  });
  var only=document.getElementById('only-failed');
  only.addEventListener('click',function(){var on=document.body.classList.toggle('only-failed');only.setAttribute('aria-pressed',on)});
  function logs(open){document.querySelectorAll('details.log').forEach(function(x){x.open=open})}
  document.getElementById('open-logs').addEventListener('click',function(){logs(true)});
  document.getElementById('close-logs').addEventListener('click',function(){logs(false)});
})();
</script>
</body></html>
`
