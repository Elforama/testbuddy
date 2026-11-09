// http against the Buddy Bench web server (bench/web): BENCH is its address, from -e.
var page = http.get(BENCH);
var missing = http.get(BENCH + 'no-such-page');
var logged = http.post(BENCH + 'log', {
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ from: 'scripting.yaml' })
});
var put = http.put(BENCH, { body: 'x' });
var del = http.delete(BENCH);
var req = http.request(BENCH, { method: 'GET' });

output.http = {
  get: page.status + ' ' + page.ok + ' ' + (page.headers['content-type'].indexOf('text/html') === 0 && page.body.indexOf('<') >= 0 ? 'html' : 'other'),
  missing: missing.status + ' ' + missing.ok,
  post: logged.status + ' ' + logged.ok,
  put: typeof put.status === 'number',
  del: typeof del.status === 'number',
  request: req.status === 200
};
console.log('http: ' + JSON.stringify(output.http));
