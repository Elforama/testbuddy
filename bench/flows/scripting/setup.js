var parsed = json('{"n": 42}');
output.setup = BASE + '-' + parsed.n;
if (GREETING !== 'hello') throw new Error('flow env missing in runScript: ' + GREETING);
console.log('setup done: ' + output.setup);
