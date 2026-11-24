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

object BinderDump {
    private val dumpers = Executors.newCachedThreadPool { r -> Thread(r, "tb-dump").apply { isDaemon = true } }
    private val gate = DumpGate { dumpers.execute(it) }

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

    fun run(name: String, args: List<String>, timeoutMs: Int): ShellResult? {
        val binder = service(name) ?: return null
        val pipe = try { ParcelFileDescriptor.createPipe() } catch (_: Throwable) { return null }
        val (read, write) = pipe[0] to pipe[1]
        try {
            return gate.run(name,
                dump = { binder.dump(write.fileDescriptor, args.toTypedArray()) },
                closeWrite = { try { write.close() } catch (_: Throwable) {} },
                read = { readAll(read.fileDescriptor, timeoutMs) })
        } catch (t: Throwable) {
            Log.w(TAG, "dump $name", t)
            return null
        } finally {
            try { read.close() } catch (_: Throwable) {}
        }
    }

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
            if (n <= 0) return out.toByteArray() to false
            out.write(buf, 0, n)
        }
    }
}
