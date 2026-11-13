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
        device.answer = { if (it.endsWith("--proto")) ShellResult(1, "", false) else ShellResult(0, composeMultiLine, false) }
        device.shells.clear()
        assertEquals(0x28001, EditorSettings.read(device)!!.inputType)
        assertEquals(listOf("dumpsys input_method --proto", "dumpsys input_method"), device.shells)
    }

    // Issue #143: the proto dump holds the same EditorInfo as the text one, in ~15 ms, not ~250.
    private fun proto(b: ByteArray) = FakeDevice().apply {
        answer = { if (it.endsWith("--proto")) ShellResult(0, String(b, Charsets.UTF_8), false, b) else ShellResult(0, composeMultiLine, false) }
    }

    @Test
    fun readsTheCurrentEditorFromTheProtoDumpAlone() {
        val device = proto(ImeTrace.singleLineCompose)
        val s = EditorSettings.read(device)!!
        assertEquals(EditorSettings("dev.testbuddy.bench", 0x8001, 0x2000006), s)
        assertFalse(s.enterIsNewline)
        assertEquals(listOf("dumpsys input_method --proto"), device.shells)
    }

    @Test
    fun aMultiLineComposeFieldInTheProtoDumpTakesANewline() {
        // The settings the text dump shows for the multi-line Compose field (issue #32).
        val s = EditorSettings.read(proto(ImeTrace.of(0x28001, 0x42000000, "dev.testbuddy.bench")))!!
        assertEquals(EditorSettings("dev.testbuddy.bench", 0x28001, 0x42000000), s)
        assertTrue(s.enterIsNewline)
        assertEquals(0x80000006.toInt(), EditorSettings.read(proto(ImeTrace.of(0x1, 0x80000006.toInt(), "a.b")))!!.imeOptions)
        assertEquals("zeros are left out", EditorSettings(null, 0, 0), EditorSettings.read(proto(ImeTrace.of(0, 0, null))))
    }

    @Test
    fun noEditorInTheProtoDumpIsUnknownWithoutTheTextDump() {
        val device = proto(ImeTrace.of(0x1, 0x6, "a.b", editor = false))
        assertNull(EditorSettings.read(device))
        assertEquals(listOf("dumpsys input_method --proto"), device.shells)
    }

    @Test
    fun aTraceLaidOutOtherwiseOrCutShortFallsBackToTheTextDump() {
        val whole = ImeTrace.singleLineCompose
        for (b in listOf(whole.copyOf(whole.size - 40), whole.copyOf(20))) {
            val device = proto(b)
            assertEquals(0x28001, EditorSettings.read(device)!!.inputType)
            assertEquals(listOf("dumpsys input_method --proto", "dumpsys input_method"), device.shells)
        }
    }

    @Test
    fun aSystemThatPrintsTextForTheProtoOptionIsReadOnce() {
        val device = FakeDevice().apply { shellResult = ShellResult(0, composeMultiLine, false) }
        assertEquals(0x28001, EditorSettings.read(device)!!.inputType)
        assertEquals(listOf("dumpsys input_method --proto"), device.shells)
    }

    // Issue #70: before Android 14 the input method service prints no current EditorInfo under
    // curEditorInfo (nor, as far as AOSP's dump code shows, under the two names guessed with
    // it). The text dump still holds the focused editor's EditorInfo twice: the app's input
    // client prints it as `mCurrentTextBoxAttribute:` (InputMethodManager.dump, Android 11-13;
    // `mCurrentEditorInfo:` from 14), the keyboard as `mInputEditorInfo:`
    // (InputMethodService.dump). parse found neither and returned null, and Enter in a
    // multi-line Compose field ran the IME action again: the bug #32 fixed. Built from AOSP's
    // dump code (EditorInfo.dump's lines), not captured on a device.
    private val clientSection = """
        |Client:
        |  InputMethodManager:
        |    mCurrentTextBoxAttribute:
        |      inputType=0x28001 imeOptions=0x42000000 privateImeOptions=null
        |      actionLabel=null actionId=0
        |      initialSelStart=3 initialSelEnd=3 initialCapsMode=0x0
        |      hintText=null label=null
        |      packageName=dev.testbuddy.bench autofillId=1073741824 fieldId=-1 fieldName=null
        |      extras=null
        |""".trimMargin()
    private val keyboardSection = """
        |Input method service:
        |  InputMethodService:
        |    mInputStarted=true mInputViewStarted=true mCandidatesViewStarted=false
        |    mInputEditorInfo:
        |      inputType=0x28001 imeOptions=0x42000000 privateImeOptions=null
        |      actionLabel=null actionId=0
        |      initialSelStart=3 initialSelEnd=3 initialCapsMode=0x0
        |      hintText=null label=null
        |      packageName=dev.testbuddy.bench autofillId=1073741824 fieldId=-1 fieldName=null
        |""".trimMargin()
    private val serviceSection = """
        |CURRENT INPUT METHOD MANAGER SERVICE STATE:
        |  mCurMethodId=com.google.android.inputmethod.latin/com.android.inputmethod.latin.LatinIME
        |  mCurClient=ClientState{6b1a4c0 uid=10214 pid=10987 displayId=0} mCurSeq=12
        |  mStartInputHistory:
        |    StartInput #11:
        |      editorInfo: inputType=0x8001 imeOptions=0x2000006 fieldId=0x0 fieldName=null actionId=0 actionLabel=null
        |""".trimMargin()
    private val api33MultiLine = serviceSection + clientSection + keyboardSection
    private val multiLine = EditorSettings("dev.testbuddy.bench", 0x28001, 0x42000000)

    @Test
    fun beforeAndroid14TheAppsOwnCopyOfTheEditorInfoIsRead() {
        assertEquals("issue #70: no current editor found in an Android 13 dump", multiLine, EditorSettings.parse(api33MultiLine))
        assertTrue(EditorSettings.parse(api33MultiLine)!!.enterIsNewline)
        // Android 14's name for the client's copy.
        assertEquals(multiLine, EditorSettings.parse(api33MultiLine.replace("mCurrentTextBoxAttribute:", "mCurrentEditorInfo:")))
    }

    @Test
    fun theKeyboardsCopyIsReadWhenTheClientPrintsNone() {
        assertEquals(multiLine, EditorSettings.parse(serviceSection + keyboardSection))
        val nullClient = "Client:\n  InputMethodManager:\n    mCurrentTextBoxAttribute: null\n    mServedConnecting=false\n"
        assertEquals(multiLine, EditorSettings.parse(serviceSection + nullClient + keyboardSection))
        assertNull("both null", EditorSettings.parse(nullClient + "    mInputEditorInfo: null\n    mShowInputRequested=false\n"))
    }

    @Test
    fun theServicesOwnCurrentEditorComesFirst() {
        // The service's copy is the current one for the whole system: preferred where printed.
        val service = "  curEditorInfo:\n    inputType=0x8001 imeOptions=0x2000006\n    packageName=dev.testbuddy.bench\n"
        assertEquals(0x8001, EditorSettings.parse(service + clientSection + keyboardSection)!!.inputType)
        assertEquals(0x8001, EditorSettings.parse(keyboardSection + clientSection + service)!!.inputType)
        // The app's copy before the keyboard's: the keyboard may not have moved to the editor yet.
        val behind = keyboardSection.replace("inputType=0x28001 imeOptions=0x42000000", "inputType=0x8001 imeOptions=0x2000006")
        assertEquals(multiLine, EditorSettings.parse(behind + clientSection))
    }

    @Test
    fun anAndroid13DeviceWhoseProtoOptionPrintsTextReadsItsEditor() {
        // Issue #70, on a device faked as API 33: its proto option gives no trace (text, or an
        // error), and the text dump decides.
        val asText = FakeDevice().apply { sdk = 33; shellResult = ShellResult(0, api33MultiLine, false) }
        assertEquals(multiLine, EditorSettings.read(asText))
        val noProto = FakeDevice().apply {
            sdk = 33
            answer = { if (it.endsWith("--proto")) ShellResult(255, "", false) else ShellResult(0, api33MultiLine, false) }
        }
        assertEquals(multiLine, EditorSettings.read(noProto))
    }
}
