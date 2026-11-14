package dev.testbuddy.agent

import android.os.IBinder
import android.os.ParcelFileDescriptor
import android.os.SystemClock
import android.system.ErrnoException
import android.system.Os
import android.system.OsConstants
import android.system.StructPollfd
import android.util.Log
import java.io.ByteArrayOutputStream
import java.util.concurrent.Executors

/**
 * `dumpsys <service> <args>` without a shell process (issue #99): the system service's binder
 * is asked for its dump in the agent's own process, the call `dumpsys` itself makes
 * (IBinder.dump on a pipe, read until the service closes it). The agent runs as the shell user,
 * so it holds the DUMP permission `dumpsys` relies on, and the output is the same.
 *
 * As in `dumpsys`, the dump runs on a thread of its own while this one reads the pipe, with a
 * timeout: a text dump can be larger than the pipe holds, and a service can be slow to answer
 * (the input method's text dump can wait on the app's main thread). A dump past the timeout is
 * left to end on its own; its thread is a pooled one, so a stuck dump does not hold up the next.
 */
object BinderDump {
    private val dumpers = Executors.newCachedThreadPool { r -> Thread(r, "tb-dump").apply { isDaemon = true } }

    /** The service's binder, null when there is none (ServiceManager is a hidden API). */
    private fun service(name: String): IBinder? = try {
        Class.forName("android.os.ServiceManager").getMethod("getService", String::class.java).invoke(null, name) as? IBinder
    } catch (t: Throwable) {
        Log.w(TAG, "dump: no binder for $name", t)
        null
    }

    /** The dump, or null when it could not be asked for (the caller runs `dumpsys` then). */
    fun run(name: String, args: List<String>, timeoutMs: Int): ShellResult? {
        val binder = service(name) ?: return null
        val pipe = try { ParcelFileDescriptor.createPipe() } catch (t: Throwable) { return null }
        val (read, write) = pipe[0] to pipe[1]
        try {
            dumpers.execute {
                // The service gets its own copy of the write end; ours is closed once the call
                // returns, so the reader sees the end of the output when the service is done.
                try { binder.dump(write.fileDescriptor, args.toTypedArray()) } catch (t: Throwable) { Log.w(TAG, "dump $name", t) }
                finally { try { write.close() } catch (_: Throwable) {} }
            }
        } catch (t: Throwable) {
            try { write.close() } catch (_: Throwable) {}
            try { read.close() } catch (_: Throwable) {}
            return null
        }
        val out = ByteArrayOutputStream()
        var timedOut = false
        try {
            val fd = read.fileDescriptor
            val buf = ByteArray(64 * 1024)
            val deadline = SystemClock.uptimeMillis() + timeoutMs
            while (true) {
                val left = deadline - SystemClock.uptimeMillis()
                if (left <= 0) { timedOut = true; break }
                val poll = StructPollfd().apply { this.fd = fd; events = OsConstants.POLLIN.toShort() }
                val ready = try { Os.poll(arrayOf(poll), left.toInt()) } catch (e: ErrnoException) {
                    if (e.errno == OsConstants.EINTR) continue else throw e
                }
                if (ready == 0) { timedOut = true; break }
                val n = Os.read(fd, buf, 0, buf.size)
                if (n <= 0) break // the end: every write end closed
                out.write(buf, 0, n)
            }
        } catch (t: Throwable) {
            Log.w(TAG, "dump $name: reading", t)
            if (out.size() == 0) return null.also { try { read.close() } catch (_: Throwable) {} }
            timedOut = true // cut short: what came is not the whole dump
        } finally {
            // Closing the read end makes a dump still writing fail (EPIPE) and end.
            try { read.close() } catch (_: Throwable) {}
        }
        val raw = out.toByteArray()
        return ShellResult(if (timedOut) -1 else 0, String(raw, Charsets.UTF_8), timedOut, raw)
    }
}
