package dev.testbuddy.agent

import java.util.concurrent.LinkedBlockingQueue

class Outbox(private val write: (String) -> Unit) {
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
                try { write(msg) } catch (_: Throwable) {}
            }
        } catch (_: InterruptedException) {}
    }

    companion object {
        const val QUEUE_MAX = 4096
    }
}

fun greet(events: Events, outbox: Outbox, hello: () -> String, send: (String) -> Unit) {
    events.sink = outbox::offer
    send(hello())
    outbox.start()
}
