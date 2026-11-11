package dev.testbuddy.agent

import android.text.InputType
import android.view.inputmethod.EditorInfo

/**
 * What the focused editor asked of the keyboard: its EditorInfo, as the input method service
 * holds it (`dumpsys input_method`). The accessibility node does not always say what kind of
 * editor it is (a multi-line Jetpack Compose field reports neither isMultiLine nor an input
 * type), but the editor always tells the keyboard.
 */
data class EditorSettings(val pkg: String?, val inputType: Int, val imeOptions: Int) {
    /**
     * Whether a keyboard's Enter types a new line here rather than run the editor's action:
     * the editor asks for no Enter action, or takes multi-line text.
     */
    val enterIsNewline: Boolean
        get() = (imeOptions and EditorInfo.IME_FLAG_NO_ENTER_ACTION) != 0 ||
            ((inputType and InputType.TYPE_MASK_CLASS) == InputType.TYPE_CLASS_TEXT &&
                (inputType and InputType.TYPE_TEXT_FLAG_MULTI_LINE) != 0)

    companion object {
        /** The current editor's section: `curEditorInfo:` (API 34+), older names before. */
        private val header = Regex("""^\s*(curEditorInfo|mCurEditorInfo|mCurAttribute)\b""")
        private val types = Regex("""\binputType=0x([0-9a-fA-F]+)\s+imeOptions=0x([0-9a-fA-F]+)""")
        private val pkg = Regex("""\bpackageName=(\S+)""")

        /** The current editor's settings in a `dumpsys input_method` dump, null if absent. */
        fun parse(dump: String): EditorSettings? {
            val lines = dump.lines()
            val at = lines.indexOfFirst { header.containsMatchIn(it) }
            if (at < 0) return null
            // The section is a few lines; later ones list past editors (all zeros).
            val section = lines.subList(at, minOf(lines.size, at + 8))
            val t = section.firstNotNullOfOrNull { types.find(it) } ?: return null
            val p = section.firstNotNullOfOrNull { pkg.find(it) }?.groupValues?.get(1)?.takeIf { it != "null" }
            return EditorSettings(p, t.groupValues[1].toLong(16).toInt(), t.groupValues[2].toLong(16).toInt())
        }

        /**
         * Reads the focused editor's settings through the shell; null when it cannot. Read
         * afresh on every call, never kept: the editor in focus is the one the service holds now.
         *
         * The proto dump first (issue #143): it holds the input method service's own state,
         * the current EditorInfo with it, and takes about 15 ms on the bench emulator. The text
         * dump also has the keyboard app and the app's input client write all their state
         * into it (about 900 KB), 220-300 ms, and it ran before every Enter in a single-line
         * field. A system that prints no proto dump gets the text one, as before.
         */
        fun read(device: Device): EditorSettings? = try {
            val proto = device.shell("dumpsys input_method --proto", 2000)
            when {
                // A trace laid out otherwise than expected throws: the text dump decides then.
                proto.code == 0 && !proto.timedOut && ImeTraceProto.isTrace(proto.raw) ->
                    try { ImeTraceProto.editor(proto.raw) } catch (_: IllegalArgumentException) { readText(device) }
                // Printed as text anyway (the option ignored): the current editor is in it.
                proto.code == 0 && !proto.timedOut && parse(proto.out) != null -> parse(proto.out)
                else -> readText(device)
            }
        } catch (_: Throwable) {
            null
        }

        private fun readText(device: Device): EditorSettings? {
            val r = device.shell("dumpsys input_method", 2000)
            return if (r.code != 0 || r.timedOut) null else parse(r.out)
        }
    }
}

/**
 * The current editor in `dumpsys input_method --proto` (seen on API 36): an IME trace file
 * (InputMethodManagerServiceTraceFileProto) of one entry. Field numbers, from AOSP's
 * inputmethodeditortrace.proto, inputmethodmanagerservice.proto and editorinfo.proto:
 * file: magic_number = 1 (fixed64 "IMMTRACE"), entry = 2; entry: input_method_manager_service
 * = 3; service: cur_attribute = 7 (the current EditorInfo, absent with no editor); EditorInfo:
 * input_type = 1, ime_options = 2, package_name = 4. Zeros are left out, as protos do.
 */
internal object ImeTraceProto {
    private val MAGIC = "IMMTRACE".toByteArray(Charsets.US_ASCII)

    /** Whether [b] is such a trace: it opens with the magic number (field 1, fixed64). */
    fun isTrace(b: ByteArray): Boolean =
        b.size >= 9 && b[0] == 0x09.toByte() && (0 until 8).all { b[1 + it] == MAGIC[it] }

    /**
     * The current editor's settings, null when the service has none. Throws
     * IllegalArgumentException when the trace has no entry or service state, or is malformed.
     */
    fun editor(b: ByteArray): EditorSettings? {
        val entry = Reader(b, 0, b.size).message(2) ?: throw IllegalArgumentException("no trace entry")
        val service = Reader(b, entry.first, entry.last + 1).message(3) ?: throw IllegalArgumentException("no service state")
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

    /** Walks the fields of one message in b[from, to). */
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

        /** Each field: its number, its value (varints), and its bytes (length-delimited). */
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

        /** The bytes of the first length-delimited [field], null when there is none. */
        fun message(field: Int): IntRange? {
            var found: IntRange? = null
            each { n, _, range -> if (found == null && n == field && range != null) found = range }
            return found
        }
    }
}
