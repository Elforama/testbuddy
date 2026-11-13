package dev.testbuddy.agent

import java.util.concurrent.LinkedBlockingQueue

/**
 * One connection's pushed events, written in order on a thread of their own. [Events] calls
 * [offer] from the accessibility event thread and the timer thread, which then never wait on
 * the socket: a big reply on a slow adb forward cannot delay the quiet timer or "settled".
 * Nothing is written before [start] (the hello goes first: [greet]) or after [close].
 */
class Outbox(private val write: (String) -> Unit) {
    // Bounded so a host that stopped reading cannot grow it without end: dropping a "changed"
    // loses no count, since every later push carries the latest one.
    private val queue = LinkedBlockingQueue<String>(QUEUE_MAX)
    @Volatile private var open = true
    private var thread: Thread? = null

    fun offer(msg: String) {
        if (open) queue.offer(msg)
    }

    @Synchronized fun start() {
        if (!open || thread != null) return
        thread = Thread({ drain() }, "tb-push").apply { isDaemon = true; start() }
    }

    @Synchronized fun close() {
        open = false
        queue.clear()
        thread?.interrupt()
    }

    private fun drain() {
        try {
            while (open) {
                val msg = queue.take()
                if (!open) break
                try { write(msg) } catch (_: Throwable) {} // a closed socket: the reader ends the connection
            }
        } catch (_: InterruptedException) {}
    }

    companion object {
        const val QUEUE_MAX = 4096
    }
}

/**
 * Starts a connection: [events]' pushes are queued on [outbox] from now on, so none is lost,
 * but written only after the hello, which the host waits for before anything else.
 */
fun greet(events: Events, outbox: Outbox, hello: () -> String, send: (String) -> Unit) {
    events.sink = outbox::offer
    send(hello())
    outbox.start()
}
