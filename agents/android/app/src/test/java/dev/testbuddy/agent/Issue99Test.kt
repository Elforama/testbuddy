package dev.testbuddy.agent

import android.view.KeyEvent
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Issue #99: every Enter that could become the editor's IME action read the focused editor's
 * settings by running `dumpsys input_method --proto` in a new shell process (a fork of the
 * agent's whole runtime, then `sh` and `dumpsys`). The same dump is now asked of the input
 * method service in the agent's own process (a binder dump call), so N Enters run 0 shell
 * commands. The settings are still read afresh before every Enter (#99 review: no cache).
 */
class Issue99Test {
    private val clock = FakeClock(10_000)
    private val injector = FakeInjector(density = 2f)

    private fun advertising(pkg: String, onSubmit: () -> Unit) = object : Field by FakeField(value = "one") {
        override fun submitIme(): Boolean { onSubmit(); return true }
        override val packageName: CharSequence? get() = pkg
    }

    private fun result(b: ByteArray) = ShellResult(0, String(b, Charsets.ISO_8859_1), false, b)

    /** What `dumpsys input_method --proto` prints on [sdk] for this editor. */
    private fun protoDump(sdk: Int, inputType: Int, imeOptions: Int, pkg: String): ShellResult = when {
        // Android 11 prints no proto: the text dump, the app's client section holding the editor.
        sdk <= 30 -> ShellResult(0, "Client:\n  InputMethodManager:\n    mCurrentTextBoxAttribute:\n" +
            "      inputType=0x${inputType.toString(16)} imeOptions=0x${imeOptions.toString(16)} privateImeOptions=null\n" +
            "      actionLabel=null actionId=0\n      packageName=$pkg autofillId=1073741824 fieldId=-1 fieldName=null\n", false)
        sdk <= 34 -> result(ImeTrace.bare(inputType, imeOptions, pkg))
        else -> result(ImeTrace.of(inputType, imeOptions, pkg))
    }

    /** A device whose input method service holds this editor, by shell and in-process alike. */
    private fun device(sdk: Int, inputType: Int, imeOptions: Int, pkg: String) = FakeDevice().apply {
        this.sdk = sdk
        val dump = protoDump(sdk, inputType, imeOptions, pkg)
        answer = { cmd -> if (cmd == "dumpsys input_method --proto") dump else ShellResult(0, "", false) }
        dumpAnswer = { call -> if (call == "input_method --proto") dump else null }
    }

    @Test
    fun enterInAMultiLineComposeFieldRunsNoShellCommand() {
        for (sdk in 30..36) {
            val device = device(sdk, 0x28001, 0x42000000, "dev.testbuddy.bench")
            var submissions = 0
            val editor = advertising("dev.testbuddy.bench") { submissions++ }
            val input = Input(injector, clock, { EditorSettings.read(device, it) }) { editor }
            repeat(5) { assertEquals("API $sdk Enter ${it + 1}", "hardware", input.key(KeyEvent.KEYCODE_ENTER)) }
            assertEquals("API $sdk: a multi-line field's Enter types a new line", 0, submissions)
            assertEquals("API $sdk: issue #99: shell commands for 5 Enters: ${device.shells}", 0, device.shells.size)
        }
    }

    @Test
    fun enterInASingleLineDoneFieldRunsNoShellCommand() {
        for (sdk in 30..36) {
            val device = device(sdk, 0x8001, 0x2000006, "dev.testbuddy.bench")
            var submissions = 0
            val editor = advertising("dev.testbuddy.bench") { submissions++ }
            val input = Input(injector, clock, { EditorSettings.read(device, it) }) { editor }
            repeat(5) { assertEquals("API $sdk Enter ${it + 1}", "editor-action", input.key(KeyEvent.KEYCODE_ENTER)) }
            assertEquals("API $sdk", 5, submissions)
            assertTrue("API $sdk: AND-006: no key after the action", injector.keys.isEmpty())
            assertEquals("API $sdk: issue #99: shell commands for 5 Enters: ${device.shells}", 0, device.shells.size)
        }
    }

    @Test
    fun eachEnterStillReadsTheServiceAfresh() {
        // #99 review: no answer kept per editor. A Compose field restarting input with other
        // settings on the same node is followed at the next Enter.
        var multiLine = false
        val device = FakeDevice().apply {
            sdk = 33
            dumpAnswer = { call ->
                if (call != "input_method --proto") null
                else result(if (multiLine) ImeTrace.bare(0x28001, 0x42000000, "dev.app") else ImeTrace.bare(0x8001, 0x2000006, "dev.app"))
            }
        }
        val input = Input(injector, clock, { EditorSettings.read(device, it) }) { advertising("dev.app") {} }
        repeat(2) { assertEquals("editor-action", input.key(KeyEvent.KEYCODE_ENTER)) }
        multiLine = true
        assertEquals("hardware", input.key(KeyEvent.KEYCODE_ENTER))
        assertEquals(List(3) { "input_method --proto" }, device.dumps)
        assertTrue(device.shells.isEmpty())
    }

    @Test
    fun whereTheServiceCannotBeAskedInProcessDumpsysRunsAsBefore() {
        val device = device(33, 0x28001, 0x42000000, "dev.app").apply { dumpAnswer = { null } }
        val input = Input(injector, clock, { EditorSettings.read(device, it) }) { advertising("dev.app") {} }
        assertEquals("hardware", input.key(KeyEvent.KEYCODE_ENTER))
        assertEquals(listOf("dumpsys input_method --proto"), device.shells)
    }

    @Test
    fun aProtoDumpCutShortInProcessFallsBackToTheTextDumpInProcess() {
        val text = "Client:\n  InputMethodManager:\n    mCurrentEditorInfo:\n" +
            "      inputType=0x28001 imeOptions=0x42000000 privateImeOptions=null\n" +
            "      actionLabel=null actionId=0\n      packageName=dev.app autofillId=1073741824 fieldId=-1 fieldName=null\n"
        val device = FakeDevice().apply {
            sdk = 34
            dumpAnswer = { call -> if (call == "input_method --proto") ShellResult(-1, "", true) else ShellResult(0, text, false) }
        }
        assertEquals(EditorSettings("dev.app", 0x28001, 0x42000000), EditorSettings.read(device, "dev.app"))
        assertEquals(listOf("input_method --proto", "input_method"), device.dumps)
        assertTrue(device.shells.isEmpty())
    }
}
