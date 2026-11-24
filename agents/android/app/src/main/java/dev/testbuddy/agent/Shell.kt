package dev.testbuddy.agent

import android.util.Log
import java.io.ByteArrayOutputStream
import java.io.File
import java.util.concurrent.TimeUnit

object Shell {
    const val READER = "tb-shell-reader"

    fun run(cmd: String, timeoutMs: Int): ShellResult {
        val p = ProcessBuilder("sh", "-c", cmd).redirectErrorStream(true).start()
        val outBytes = ByteArrayOutputStream()
        val reader = Thread({
            try { p.inputStream.copyTo(outBytes) } catch (_: Throwable) {}
        }, READER).apply { isDaemon = true; start() }
        val waiter = Thread {
            try { p.waitFor() } catch (_: InterruptedException) {}
        }.apply { isDaemon = true; start() }
        waiter.join(timeoutMs.toLong())
        val done = !waiter.isAlive
        if (!done) killTree(p, waiter)
        reader.join(1000)
        if (reader.isAlive) try { p.inputStream.close() } catch (_: Throwable) {}
        val raw = synchronized(outBytes) { outBytes.toByteArray() }
        return ShellResult(if (done) p.exitValue() else -1, String(raw, Charsets.UTF_8), !done, raw)
    }

    private fun killTree(p: Process, waiter: Thread) {
        val root = pid(p)
        if (root <= 0) Log.w(TAG, "shell: no pid for the timed-out command; only sh is killed")
        if (root > 0) {
            for (pass in 0 until 2) {
                if (!waiter.isAlive) break
                val under = try { descendants(root, parents()) } catch (t: Throwable) { Log.w(TAG, "shell: listing processes", t); emptyList() }
                if (under.isEmpty()) break
                kill(under)
            }
        }
        p.destroyForcibly()
    }

    fun descendants(root: Int, parents: Map<Int, Int>): List<Int> {
        val children = HashMap<Int, MutableList<Int>>()
        for ((pid, ppid) in parents) children.getOrPut(ppid) { ArrayList() } += pid
        val out = ArrayList<Int>()
        val todo = ArrayDeque(listOf(root))
        val seen = HashSet<Int>()
        while (todo.isNotEmpty()) {
            for (c in children[todo.removeFirst()].orEmpty()) if (c != root && seen.add(c)) { out += c; todo += c }
        }
        return out
    }

    private fun parents(): Map<Int, Int> {
        val out = HashMap<Int, Int>()
        File("/proc").listFiles()?.forEach { dir ->
            val pid = dir.name.toIntOrNull() ?: return@forEach
            val stat = try { File(dir, "stat").readText() } catch (_: Throwable) { return@forEach }
            out[pid] = parentInStat(stat) ?: return@forEach
        }
        if (out.isNotEmpty()) return out
        val ps = ProcessBuilder("ps", "-A", "-o", "pid=", "-o", "ppid=").redirectErrorStream(true).start()
        ps.inputStream.bufferedReader().forEachLine { line ->
            val f = line.trim().split(Regex("\\s+"))
            val pid = f.getOrNull(0)?.toIntOrNull()
            val ppid = f.getOrNull(1)?.toIntOrNull()
            if (pid != null && ppid != null) out[pid] = ppid
        }
        ps.waitFor(1, TimeUnit.SECONDS)
        return out
    }

    fun parentInStat(stat: String): Int? {
        val end = stat.lastIndexOf(')')
        if (end < 0) return null
        return stat.substring(end + 1).trim().split(Regex("\\s+")).getOrNull(1)?.toIntOrNull()
    }

    private fun kill(pids: List<Int>) {
        try {
            ProcessBuilder(listOf("kill", "-9") + pids.map { it.toString() }).redirectErrorStream(true).start().apply {
                inputStream.readBytes()
                waitFor()
            }
        } catch (t: Throwable) { Log.w(TAG, "shell: kill $pids", t) }
    }

    private fun pid(p: Process): Int {
        try { return (Process::class.java.getMethod("pid").invoke(p) as Number).toInt() } catch (_: Throwable) {}
        var c: Class<*>? = p.javaClass
        while (c != null) {
            try { return c.getDeclaredField("pid").apply { isAccessible = true }.getInt(p) } catch (_: Throwable) {}
            c = c.superclass
        }
        return -1
    }
}
