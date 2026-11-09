// Timers run after this code, in due order, before the step ends. A cleared timeout never runs.
output.order = [];
output.ticks = 0;
setTimeout(function () { output.order.push('late'); }, 60);
setTimeout(function () { output.order.push('early'); }, 20);
var cancelled = setTimeout(function () { output.order.push('cancelled'); }, 40);
clearTimeout(cancelled);
var id = setInterval(function () {
  output.ticks++;
  if (output.ticks === 3) clearInterval(id);
}, 5);
output.order.push('sync');
