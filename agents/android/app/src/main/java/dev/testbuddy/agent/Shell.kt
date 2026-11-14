package dev.testbuddy.agent

import android.util.Log
import java.io.ByteArrayOutputStream
import java.io.File

/**
 * Runs a shell command as the agent's user (the shell user on the device).
 *
 * The command's end is waited for by a plain waitFor() on a thread of its own, joined with
 * the timeout. Android's waitFor(timeout) checks for the end only every 100 ms: a command
 * done in 15 ms answered after 100 to 160 ms (issue #143, measured on the bench emulator).
 *
 * A command that runs past its timeout is killed with everything it started (issue #74):
 * killing `sh` alone left its children running, holding the output pipe open, and with it the
 * thread reading it.
 */
object Shell {
    /** The name of the thread that reads a command's output. */
    const val READER = "tb-shell-reader"

    fun run(cmd: String, timeoutMs: Int): ShellResult {
        val p = ProcessBuilder("sh", "-c", cmd).redirectErrorStream(true).start()
        val outBytes = ByteArrayOutputStream()
        val reader = Thread({
            try { p.inputStream.copyTo(outBytes) } catch (_: Throwable) {} // closed below
        }, READER).apply { isDaemon = true; start() }
        val waiter = Thread {
            try { p.waitFor() } catch (_: InterruptedException) {}
        }.apply { isDaemon = true; start() }
        waiter.join(timeoutMs.toLong())
        val done = !waiter.isAlive
        if (!done) killTree(p)
        reader.join(1000)
        // A child that outlived the command (one it left running in the background) can still
        // hold the pipe: the reply does not wait for it, and the reader is let go.
        if (reader.isAlive) try { p.inputStream.close() } catch (_: Throwable) {}
        val raw = synchronized(outBytes) { outBytes.toByteArray() }
        return ShellResult(if (done) p.exitValue() else -1, String(raw, Charsets.UTF_8), !done, raw)
    }

    /**
     * Kills [p]'s `sh` and every process under it. The children are found, and killed, while
     * `sh` still lives: once it is gone they belong to init and nothing links them to it. Twice,
     * for a child started while the first were being listed.
     */
    private fun killTree(p: Process) {
        val root = pid(p)
        if (root > 0) {
            for (pass in 0 until 2) {
                val under = try { descendants(root, parents()) } catch (t: Throwable) { Log.w(TAG, "shell: listing processes", t); emptyList() }
                if (under.isEmpty()) break
                kill(under)
            }
        }
        p.destroyForcibly()
    }

    /** Every process under [root], given each process's parent. */
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

    /** Each visible process's parent: from /proc (Linux, the device), else `ps` (a test's host). */
    private fun parents(): Map<Int, Int> {
        val out = HashMap<Int, Int>()
        File("/proc").listFiles()?.forEach { dir ->
            val pid = dir.name.toIntOrNull() ?: return@forEach
            // "pid (comm) state ppid ...": comm can hold spaces and parentheses.
            val stat = try { File(dir, "stat").readText() } catch (_: Throwable) { return@forEach }
            val ppid = stat.substring(stat.lastIndexOf(')') + 1).trim().split(' ').getOrNull(1)?.toIntOrNull() ?: return@forEach
            out[pid] = ppid
        }
        if (out.isNotEmpty()) return out
        val ps = ProcessBuilder("ps", "-A", "-o", "pid=", "-o", "ppid=").redirectErrorStream(true).start()
        ps.inputStream.bufferedReader().forEachLine { line ->
            val f = line.trim().split(Regex("\\s+"))
            val pid = f.getOrNull(0)?.toIntOrNull()
            val ppid = f.getOrNull(1)?.toIntOrNull()
            if (pid != null && ppid != null) out[pid] = ppid
        }
        ps.waitFor()
        return out
    }

    private fun kill(pids: List<Int>) {
        try {
            ProcessBuilder(listOf("kill", "-9") + pids.map { it.toString() }).redirectErrorStream(true).start().apply {
                inputStream.readBytes()
                waitFor()
            }
        } catch (t: Throwable) { Log.w(TAG, "shell: kill $pids", t) }
    }

    /** The process's id: Process.pid() where there is one (Java 9+), else Android's field. */
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
