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
import java.io.FileDescriptor
import java.lang.reflect.Method
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
 * left to end on its own: when the service returns, or when system_server's TransferPipe
 * timeout ends its wait on the app or keyboard it is dumping. Until then no other dump of that
 * service starts ([DumpGate]): one stuck thread at most per service.
 */
object BinderDump {
    private val dumpers = Executors.newCachedThreadPool { r -> Thread(r, "tb-dump").apply { isDaemon = true } }
    private val gate = DumpGate { dumpers.execute(it) }

    /** ServiceManager.getService (a hidden API), looked up once; null when it cannot be. */
    private val getService: Method? by lazy {
        try {
            Class.forName("android.os.ServiceManager").getMethod("getService", String::class.java)
        } catch (t: Throwable) {
            Log.w(TAG, "dump: no ServiceManager.getService; dumpsys runs in the shell", t)
            null
        }
    }

    private fun service(name: String): IBinder? = try {
        getService?.invoke(null, name) as? IBinder
    } catch (_: Throwable) {
        null
    }

    /** The dump, or null when it could not be made (the caller runs `dumpsys` then). */
    fun run(name: String, args: List<String>, timeoutMs: Int): ShellResult? {
        val binder = service(name) ?: return null
        val pipe = try { ParcelFileDescriptor.createPipe() } catch (_: Throwable) { return null }
        val (read, write) = pipe[0] to pipe[1]
        try {
            // The service gets its own copy of the write end; ours is closed once the call
            // returns, so the reader sees the end of the output when the service is done.
            return gate.run(name,
                dump = { binder.dump(write.fileDescriptor, args.toTypedArray()) },
                closeWrite = { try { write.close() } catch (_: Throwable) {} },
                read = { readAll(read.fileDescriptor, timeoutMs) })
        } catch (t: Throwable) {
            Log.w(TAG, "dump $name", t)
            return null
        } finally {
            // Closing the read end makes a dump still writing fail (EPIPE) and end.
            try { read.close() } catch (_: Throwable) {}
        }
    }

    /** Everything written to [fd] until every write end closes, or [timeoutMs]: (bytes, timed out). */
    private fun readAll(fd: FileDescriptor, timeoutMs: Int): Pair<ByteArray, Boolean> {
        val out = ByteArrayOutputStream()
        val buf = ByteArray(64 * 1024)
        val deadline = SystemClock.uptimeMillis() + timeoutMs
        while (true) {
            val left = deadline - SystemClock.uptimeMillis()
            if (left <= 0) return out.toByteArray() to true
            val poll = StructPollfd().apply { this.fd = fd; events = OsConstants.POLLIN.toShort() }
            val ready = try { Os.poll(arrayOf(poll), left.toInt()) } catch (e: ErrnoException) {
                if (e.errno == OsConstants.EINTR) continue else throw e
            }
            if (ready == 0) return out.toByteArray() to true
            val n = Os.read(fd, buf, 0, buf.size)
            if (n <= 0) return out.toByteArray() to false // the end: every write end closed
            out.write(buf, 0, n)
        }
    }
}
