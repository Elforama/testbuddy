package dev.testbuddy.agent

import android.net.LocalServerSocket
import android.net.LocalSocket
import android.os.SystemClock
import android.util.Log
import java.io.BufferedInputStream
import java.io.BufferedOutputStream
import java.io.OutputStream
import kotlin.system.exitProcess

/**
 * Serves one host at a time on the abstract socket [SOCKET_NAME]. A new connection replaces
 * the old one. Only the shell (adbd) and root may connect: the socket checks the peer's uid,
 * so other apps on the device cannot drive it.
 *
 * Protocol: one JSON object per line each way (see the host's internal/wire package). The
 * requests themselves are [Ops]'.
 */
class Server(private val ops: Ops, private val events: Events) {
    private val writeLock = Object()
    @Volatile private var out: OutputStream? = null
    @Volatile private var lastClientAt = SystemClock.uptimeMillis()
    private val clients = java.util.concurrent.atomic.AtomicInteger(0)

    fun serve() {
        val server = LocalServerSocket(SOCKET_NAME)
        Log.i(TAG, "listening on @$SOCKET_NAME, injection: ${ops.injectPath}")
        println("ready")
        startIdleWatch()
        var current: LocalSocket? = null
        while (true) {
            val sock = server.accept()
            val uid = try { sock.peerCredentials.uid } catch (_: Throwable) { -1 }
            if (uid != 0 && uid != 2000) {
                Log.w(TAG, "refused connection from uid $uid")
                sock.close()
                continue
            }
            try { current?.close() } catch (_: Throwable) {}
            current = sock
            Thread({ handle(sock) }, "tb-client").start()
        }
    }

    /**
     * Exits when no client has been connected for [Ops.idleLimitMs]: tb releases the device itself
     * when a run ends ("exit"), but a run that was killed (or crashed) cannot, and its agent
     * would keep the device's UiAutomation slot from other tools. ORPHAN_EXIT_MS outlasts tb's
     * reconnect window; a run with --keep-agents asks for IDLE_EXIT_MS ("keep").
     */
    private fun startIdleWatch() {
        Thread({
            while (true) {
                Thread.sleep(5_000) // housekeeping only; never on a test's path
                if (clients.get() == 0 && SystemClock.uptimeMillis() - lastClientAt > ops.idleLimitMs) {
                    Log.i(TAG, "idle, exiting")
                    ops.shutdown()
                    disableNoopService()
                    exitProcess(0)
                }
            }
        }, "tb-idle").apply { isDaemon = true }.start()
    }

    private fun handle(sock: LocalSocket) {
        clients.incrementAndGet()
        val os = BufferedOutputStream(sock.outputStream, 64 * 1024)
        synchronized(writeLock) { out = os }
        // Pushed events go out on the outbox's own thread, after the hello (Outbox.kt).
        val outbox = Outbox { msg -> send(os, msg) }
        try {
            greet(events, outbox, ops::hello) { send(os, it) }
            val reader = BufferedInputStream(sock.inputStream).bufferedReader()
            while (true) {
                val line = reader.readLine() ?: break
                val reply = ops.handle(line, sock) ?: continue
                send(os, reply.text, reply.bin)
            }
        } catch (_: Throwable) {
        } finally {
            outbox.close()
            synchronized(writeLock) { if (out === os) { out = null; events.sink = null } }
            try { sock.close() } catch (_: Throwable) {}
            ops.clientGone(sock)
            lastClientAt = SystemClock.uptimeMillis()
            clients.decrementAndGet()
        }
    }

    private fun send(os: OutputStream, line: String, bin: ByteArray? = null) {
        synchronized(writeLock) {
            os.write(line.toByteArray(Charsets.UTF_8))
            os.write('\n'.code)
            if (bin != null) os.write(bin)
            os.flush()
        }
    }

    companion object {
        const val IDLE_EXIT_MS = 30 * 60_000L
        const val ORPHAN_EXIT_MS = 60_000L
    }
}
