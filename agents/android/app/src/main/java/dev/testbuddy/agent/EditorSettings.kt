package dev.testbuddy.agent

import android.text.InputType
import android.view.inputmethod.EditorInfo

/**
 * What the focused editor asked of the keyboard: its EditorInfo, as `dumpsys input_method`
 * shows it. The accessibility node does not always say what kind of editor it is (a
 * multi-line Jetpack Compose field reports neither isMultiLine nor an input type), but the
 * editor always tells the keyboard.
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

        /** Reads the focused editor's settings through the shell; null when it cannot. */
        fun read(device: Device): EditorSettings? = try {
            val r = device.shell("dumpsys input_method", 2000)
            if (r.code != 0 || r.timedOut) null else parse(r.out)
        } catch (_: Throwable) {
            null
        }
    }
}
