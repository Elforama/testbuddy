// runScript: env from the command, shared `output`, json() helper
var parsed = json('{"n": 42}');
output.setup = BASE + '-' + parsed.n;
console.log('setup done: ' + output.setup);
