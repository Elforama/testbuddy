package dev.testbuddy.agent

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The editor settings read from dumps captured on emulators (issues #70 and #99), API 30-34
 * google_apis arm64 images, the bench's Compose toolkit screen: its multi-line field
 * (`tk_notes`, inputType=0x28001 imeOptions=0x42000000) or the single-line one (`tk_field`,
 * 0x8001 / Done 0x2000006) focused, the AOSP keyboard up. In src/test/resources/ime.
 */
class CapturedDumpsTest {
    private fun res(name: String): ByteArray =
        javaClass.classLoader!!.getResourceAsStream("ime/$name")!!.use { it.readBytes() }

    private val multiLine = EditorSettings("dev.testbuddy.bench", 0x28001, 0x42000000)
    private val singleLine = EditorSettings("dev.testbuddy.bench", 0x8001, 0x2000006)

    private fun device(sdk: Int, proto: ShellResult, text: String = "") = FakeDevice().apply {
        this.sdk = sdk
        answer = { if (it.endsWith("--proto")) proto else ShellResult(0, text, false) }
    }

    @Test
    fun android12To14PrintTheServiceStateBareAndItIsReadAlone() {
        // `dumpsys input_method --proto`: opens with 0x1a (field 3), no IMMTRACE, about 500 bytes.
        for (sdk in 31..34) {
            for ((file, want) in listOf("multiline" to multiLine, "singleline" to singleLine)) {
                val b = res("api$sdk-$file-proto.bin")
                assertFalse("API $sdk: not a trace file", ImeTraceProto.isTrace(b))
                assertTrue("API $sdk: a bare service state", ImeTraceProto.isProto(b))
                val d = device(sdk, ShellResult(0, String(b, Charsets.ISO_8859_1), false, b))
                assertEquals("API $sdk $file", want, EditorSettings.read(d, "dev.testbuddy.bench"))
                assertEquals("API $sdk: no text dump", listOf("dumpsys input_method --proto"), d.shells)
            }
        }
        assertTrue(res("api33-multiline-proto.bin").let { EditorSettings.read(device(33, ShellResult(0, "", false, it)))!!.enterIsNewline })
        assertFalse(res("api33-singleline-proto.bin").let { EditorSettings.read(device(33, ShellResult(0, "", false, it)))!!.enterIsNewline })
    }

    @Test
    fun android11PrintsTextForTheProtoOptionWithTheClientsSection() {
        // API 30: `--proto` prints the text dump. The service prints no current editor; the
        // client's `mCurrentTextBoxAttribute:` and the keyboard's `mInputEditorInfo:` hold it.
        // The excerpt also has the start-input history, whose types must not be taken.
        val text = String(res("api30-multiline-text.txt"), Charsets.UTF_8)
        assertEquals(multiLine, EditorSettings.parse(text, "dev.testbuddy.bench"))
        val d = device(30, ShellResult(0, text, false))
        assertEquals(multiLine, EditorSettings.read(d, "dev.testbuddy.bench"))
        assertEquals(listOf("dumpsys input_method --proto"), d.shells)
        // The keyboard's copy alone, as when the client section is left out.
        assertEquals(multiLine, EditorSettings.parse(text.replace("mCurrentTextBoxAttribute:", "mOther:")))
    }

    @Test
    fun android14NamesTheClientsSectionMCurrentEditorInfo() {
        val text = String(res("api34-multiline-text.txt"), Charsets.UTF_8)
        assertTrue(text.contains("  mCurrentEditorInfo:\n"))
        assertEquals(multiLine, EditorSettings.parse(text, "dev.testbuddy.bench"))
    }
}
