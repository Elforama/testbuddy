package dev.testbuddy.agent

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

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
        val text = String(res("api30-multiline-text.txt"), Charsets.UTF_8)
        assertEquals(multiLine, EditorSettings.parse(text, "dev.testbuddy.bench"))
        val d = device(30, ShellResult(0, text, false))
        assertEquals(multiLine, EditorSettings.read(d, "dev.testbuddy.bench"))
        assertEquals(listOf("dumpsys input_method --proto"), d.shells)
        assertEquals(multiLine, EditorSettings.parse(text.replace("mCurrentTextBoxAttribute:", "mOther:")))
    }

    @Test
    fun android14NamesTheClientsSectionMCurrentEditorInfo() {
        val text = String(res("api34-multiline-text.txt"), Charsets.UTF_8)
        assertTrue(text.contains("  mCurrentEditorInfo:\n"))
        assertEquals(multiLine, EditorSettings.parse(text, "dev.testbuddy.bench"))
    }
}
