package dev.testbuddy.agent

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Reading the focused editor's keyboard settings from `dumpsys input_method` (issue #32). */
class EditorSettingsTest {
    // API 36 (emulator), a multi-line Jetpack Compose field (singleLine = false) focused.
    private val composeMultiLine = """
        |      curClient=ClientState{72edcb0 mUid=10214 mPid=10987 mSelfReportedDisplayId=0}
        |      curEditorInfo:
        |        inputType=0x28001 imeOptions=0x42000000 privateImeOptions=null
        |        actionLabel=null actionId=0
        |        initialSelStart=0 initialSelEnd=0 initialToolType=1 initialCapsMode=0x0
        |        hintText=null label=null
        |        packageName=dev.testbuddy.bench autofillId=1073741824 fieldId=-1 fieldName=null
        |        hintLocales=null
        |      imeBindingState:
        |  mStartInputHistory:
        |    StartInput #216:
        |      inputType=0x0 imeOptions=0x0 fieldId=0x0 fieldName=null actionId=0 actionLabel=null
    """.trimMargin()

    @Test
    fun readsTheCurrentEditorNotTheHistory() {
        val s = EditorSettings.parse(composeMultiLine)!!
        assertEquals("dev.testbuddy.bench", s.pkg)
        assertEquals(0x28001, s.inputType)
        assertEquals(0x42000000, s.imeOptions)
        assertTrue(s.enterIsNewline)
    }

    @Test
    fun aSingleLineEditorWithADoneActionSubmits() {
        // The single-line Compose field next to it: Done, no multi-line flag.
        val s = EditorSettings.parse(composeMultiLine
            .replace("inputType=0x28001 imeOptions=0x42000000", "inputType=0x8001 imeOptions=0x2000006"))!!
        assertFalse(s.enterIsNewline)
    }

    @Test
    fun eitherTheMultiLineTypeOrNoEnterActionMeansANewline() {
        assertTrue("multi-line text", EditorSettings(null, 0x20001, 0x6).enterIsNewline)
        assertTrue("no Enter action", EditorSettings(null, 0x1, 0x40000006).enterIsNewline)
        // The multi-line flag only means text in the text class (it is another flag elsewhere).
        assertFalse("a number field", EditorSettings(null, 0x20002, 0x6).enterIsNewline)
    }

    @Test
    fun theHighFlagBitStillParses() {
        val s = EditorSettings.parse("mCurAttribute:\n  inputType=0x1 imeOptions=0x80000006\n  packageName=a.b")!!
        assertEquals(0x80000006.toInt(), s.imeOptions)
        assertEquals("a.b", s.pkg)
        assertFalse(s.enterIsNewline)
    }

    @Test
    fun noCurrentEditorIsUnknown() {
        assertNull(EditorSettings.parse(""))
        assertNull("history alone", EditorSettings.parse(composeMultiLine.replace("curEditorInfo:", "somethingElse:")))
        assertNull("a section with no types", EditorSettings.parse("curEditorInfo: null\n  imeBindingState:\n"))
    }

    @Test
    fun aMissingPackageIsNull() {
        val s = EditorSettings.parse("curEditorInfo:\n  inputType=0x1 imeOptions=0x6\n  packageName=null fieldId=-1")!!
        assertNull(s.pkg)
    }

    @Test
    fun readingFailsQuietly() {
        val device = FakeDevice().apply { shellResult = ShellResult(1, "", false) }
        assertNull(EditorSettings.read(device))
        device.shellResult = ShellResult(0, composeMultiLine, true)
        assertNull("timed out", EditorSettings.read(device))
        device.shellResult = ShellResult(0, composeMultiLine, false)
        assertEquals(0x28001, EditorSettings.read(device)!!.inputType)
        assertEquals("dumpsys input_method", device.shells.last())
    }
}
