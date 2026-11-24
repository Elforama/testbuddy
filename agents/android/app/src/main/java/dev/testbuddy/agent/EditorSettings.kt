package dev.testbuddy.agent

import android.text.InputType
import android.view.inputmethod.EditorInfo

data class EditorSettings(val pkg: String?, val inputType: Int, val imeOptions: Int) {
    val enterIsNewline: Boolean
        get() = (imeOptions and EditorInfo.IME_FLAG_NO_ENTER_ACTION) != 0 ||
            ((inputType and InputType.TYPE_MASK_CLASS) == InputType.TYPE_CLASS_TEXT &&
                (inputType and InputType.TYPE_TEXT_FLAG_MULTI_LINE) != 0)

    companion object {
        private val headers = listOf(
            Regex("""^\s*(curEditorInfo|mCurEditorInfo|mCurAttribute):(?!\s*null\b)"""),
            Regex("""^\s*(mCurrentEditorInfo|mCurrentTextBoxAttribute):(?!\s*null\b)"""),
            Regex("""^\s*mInputEditorInfo:(?!\s*null\b)"""),
        )
        private val types = Regex("""\binputType=0x([0-9a-fA-F]+)\s+imeOptions=0x([0-9a-fA-F]+)""")
        private val pkg = Regex("""\bpackageName=(\S+)""")

        fun parse(dump: String, forPkg: String? = null): EditorSettings? {
            val lines = dump.lines()
            var first: EditorSettings? = null
            for (header in headers) {
                for ((at, line) in lines.withIndex()) {
                    if (!header.containsMatchIn(line)) continue
                    val section = lines.subList(at, minOf(lines.size, at + 8))
                    val t = section.take(2).firstNotNullOfOrNull { types.find(it) } ?: continue
                    val p = section.firstNotNullOfOrNull { pkg.find(it) }?.groupValues?.get(1)?.takeIf { it != "null" }
                    val s = EditorSettings(p, t.groupValues[1].toLong(16).toInt(), t.groupValues[2].toLong(16).toInt())
                    if (forPkg == null || p == forPkg) return s
                    if (first == null) first = s
                }
            }
            return first
        }

        fun read(device: Device, forPkg: String? = null): EditorSettings? = try {
            val proto = dump(device, listOf("--proto"))
            when {
                proto.timedOut -> null
                proto.code == 0 && !proto.timedOut && ImeTraceProto.isProto(proto.raw) ->
                    try { ImeTraceProto.editor(proto.raw) } catch (_: IllegalArgumentException) { readText(device, forPkg) }
                proto.code == 0 && !proto.timedOut && parse(proto.out, forPkg) != null -> parse(proto.out, forPkg)
                else -> readText(device, forPkg)
            }
        } catch (_: Throwable) {
            null
        }

        private fun readText(device: Device, forPkg: String?): EditorSettings? {
            val r = dump(device, emptyList())
            return if (r.code != 0 || r.timedOut) null else parse(r.out, forPkg)
        }

        private fun dump(device: Device, args: List<String>): ShellResult =
            device.dump("input_method", args, 2000)
                ?: device.shell((listOf("dumpsys input_method") + args).joinToString(" "), 2000)
    }
}

internal object ImeTraceProto {
    private val MAGIC = "IMMTRACE".toByteArray(Charsets.US_ASCII)
    private const val SERVICE_KEY = 0x1a

    fun isTrace(b: ByteArray): Boolean =
        b.size >= 9 && b[0] == 0x09.toByte() && (0 until 8).all { b[1 + it] == MAGIC[it] }

    fun isProto(b: ByteArray): Boolean = isTrace(b) || (b.isNotEmpty() && b[0] == SERVICE_KEY.toByte())

    fun editor(b: ByteArray): EditorSettings? {
        val service = if (isTrace(b)) {
            val entry = Reader(b, 0, b.size).message(2) ?: throw IllegalArgumentException("no trace entry")
            Reader(b, entry.first, entry.last + 1).message(3) ?: throw IllegalArgumentException("no service state")
        } else {
            var other = false
            Reader(b, 0, b.size).each { field, _, _ -> if (field != 3) other = true }
            require(!other) { "not a bare service state" }
            Reader(b, 0, b.size).message(3) ?: throw IllegalArgumentException("no service state")
        }
        val info = Reader(b, service.first, service.last + 1).message(7) ?: return null
        var inputType = 0
        var imeOptions = 0
        var pkg: String? = null
        Reader(b, info.first, info.last + 1).each { field, value, range ->
            when (field) {
                1 -> inputType = value.toInt()
                2 -> imeOptions = value.toInt()
                4 -> pkg = range?.let { String(b, it.first, it.last + 1 - it.first, Charsets.UTF_8) }
            }
        }
        return EditorSettings(pkg?.takeIf { it.isNotEmpty() && it != "null" }, inputType, imeOptions)
    }

    private class Reader(private val b: ByteArray, private var at: Int, private val to: Int) {
        private fun byte(): Int {
            require(at < to) { "truncated" }
            return b[at++].toInt() and 0xff
        }

        private fun varint(): Long {
            var v = 0L
            var shift = 0
            while (true) {
                require(shift < 64) { "varint too long" }
                val c = byte()
                v = v or ((c and 0x7f).toLong() shl shift)
                if (c < 0x80) return v
                shift += 7
            }
        }

        private fun skip(n: Int) {
            require(n in 0..(to - at)) { "truncated" }
            at += n
        }

        fun each(f: (field: Int, value: Long, range: IntRange?) -> Unit) {
            while (at < to) {
                val key = varint()
                val field = (key ushr 3).toInt()
                when ((key and 7).toInt()) {
                    0 -> f(field, varint(), null)
                    1 -> skip(8)
                    2 -> {
                        val n = varint()
                        require(n in 0..(to - at).toLong()) { "bad length" }
                        val start = at
                        skip(n.toInt())
                        f(field, 0, start until start + n.toInt())
                    }
                    5 -> skip(4)
                    else -> throw IllegalArgumentException("unknown wire type")
                }
            }
        }

        fun message(field: Int): IntRange? {
            var found: IntRange? = null
            each { n, _, range -> if (found == null && n == field && range != null) found = range }
            return found
        }
    }
}
