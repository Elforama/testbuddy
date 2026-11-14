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
        /**
         * The sections that hold the current editor's EditorInfo, the most authoritative first.
         * The input method service's own: `curEditorInfo:` (seen on API 36), the other two
         * names unconfirmed. The app's input client's (issue #70): `mCurrentTextBoxAttribute:`
         * before Android 14 (InputMethodManager.dump; the service prints no current editor
         * there), `mCurrentEditorInfo:` from 14. The keyboard's own (InputMethodService.dump),
         * last: it can still hold the editor before while it moves to a new one.
         */
        private val headers = listOf(
            Regex("""^\s*(curEditorInfo|mCurEditorInfo|mCurAttribute):(?!\s*null\b)"""),
            Regex("""^\s*(mCurrentEditorInfo|mCurrentTextBoxAttribute):(?!\s*null\b)"""),
            Regex("""^\s*mInputEditorInfo:(?!\s*null\b)"""),
        )
        private val types = Regex("""\binputType=0x([0-9a-fA-F]+)\s+imeOptions=0x([0-9a-fA-F]+)""")
        private val pkg = Regex("""\bpackageName=(\S+)""")

        /**
         * The current editor's settings in a `dumpsys input_method` dump, null if absent. With
         * [forPkg] (the focused field's app), a section of that app is preferred: Android 11-13
         * also dump the client of the last focused window "just in case", a second client
         * section that can come first and hold another app's editor. A section of the app in a
         * later group wins over another app's in an earlier one; with none, the first found.
         */
        fun parse(dump: String, forPkg: String? = null): EditorSettings? {
            val lines = dump.lines()
            var first: EditorSettings? = null
            for (header in headers) {
                for ((at, line) in lines.withIndex()) {
                    // A section printed as `name: null` (no editor) does not match its header.
                    if (!header.containsMatchIn(line)) continue
                    // The section is EditorInfo.dump's few lines under its header (or on its
                    // line): the types first, the package within the next few. Types further
                    // down belong to something else (the start-input history lists past editors).
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

        /**
         * Reads the focused editor's settings from the input method service; null when it
         * cannot. Read afresh on every call, never kept (issue #99 review: see
         * Input.enterIsNewline). [forPkg], the focused field's app, picks its section where the
         * text dump has several.
         *
         * Each dump is asked of the service in the agent's own process (Device.dump: the same
         * call `dumpsys` makes, with the same output), with no shell process (issue #99): a
         * shell command forks the agent's whole runtime and then runs `sh` and `dumpsys`. Where
         * the service cannot be reached that way, `dumpsys` runs in the shell as before.
         *
         * The proto dump first (issue #143): it holds the input method service's own state,
         * the current EditorInfo with it, and takes about 15 ms on the bench emulator. Android
         * 15+ print it as a trace file of one entry; 12-14 print the service's state bare
         * (issue #99 review). The text dump also has the keyboard app and the app's input
         * client write all their state into it (about 900 KB), 220-300 ms, and can wait on the
         * app's main thread. A system that prints no proto dump (Android 11) gets the text one.
         */
        fun read(device: Device, forPkg: String? = null): EditorSettings? = try {
            val proto = dump(device, listOf("--proto"))
            when {
                // A proto laid out otherwise than expected throws: the text dump decides then.
                proto.code == 0 && !proto.timedOut && ImeTraceProto.isProto(proto.raw) ->
                    try { ImeTraceProto.editor(proto.raw) } catch (_: IllegalArgumentException) { readText(device, forPkg) }
                // Printed as text anyway (the option ignored): the current editor is in it.
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

        /** `dumpsys input_method [args]`: in the agent's process, else in the shell. */
        private fun dump(device: Device, args: List<String>): ShellResult =
            device.dump("input_method", args, 2000)
                ?: device.shell((listOf("dumpsys input_method") + args).joinToString(" "), 2000)
    }
}

/**
 * The current editor in `dumpsys input_method --proto`. Android 15+ (seen on API 36) print an
 * IME trace file (InputMethodManagerServiceTraceFileProto) of one entry; Android 12-14 print
 * the service's state alone, as the entry's field 3 with nothing around it (dumpAsProtoNoCheck:
 * `dumpDebug(proto, InputMethodManagerServiceTraceProto.INPUT_METHOD_MANAGER_SERVICE)`).
 * Field numbers, from AOSP's inputmethodeditortrace.proto, inputmethodmanagerservice.proto and
 * editorinfo.proto (the same in 12-16): file: magic_number = 1 (fixed64 "IMMTRACE"), entry =
 * 2; entry: input_method_manager_service = 3; service: cur_attribute = 7 (the current
 * EditorInfo, absent with no editor); EditorInfo: input_type = 1, ime_options = 2,
 * package_name = 4. Zeros are left out, as protos do.
 */
internal object ImeTraceProto {
    private val MAGIC = "IMMTRACE".toByteArray(Charsets.US_ASCII)
    /** The key of field 3, length-delimited: a bare service state opens with it. */
    private const val SERVICE_KEY = 0x1a

    /** Whether [b] is a trace file: it opens with the magic number (field 1, fixed64). */
    fun isTrace(b: ByteArray): Boolean =
        b.size >= 9 && b[0] == 0x09.toByte() && (0 until 8).all { b[1 + it] == MAGIC[it] }

    /** Whether [b] is a trace file or a bare service state (Android 12-14). Text opens otherwise. */
    fun isProto(b: ByteArray): Boolean = isTrace(b) || (b.isNotEmpty() && b[0] == SERVICE_KEY.toByte())

    /**
     * The current editor's settings, null when the service has none. Throws
     * IllegalArgumentException when the proto has no entry or service state, or is malformed.
     */
    fun editor(b: ByteArray): EditorSettings? {
        val service = if (isTrace(b)) {
            val entry = Reader(b, 0, b.size).message(2) ?: throw IllegalArgumentException("no trace entry")
            Reader(b, entry.first, entry.last + 1).message(3) ?: throw IllegalArgumentException("no service state")
        } else {
            // Bare: the service's state is all there is (each field walked: a cut one throws).
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
