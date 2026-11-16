package dev.testbuddy.agent

import android.text.InputType
import android.view.KeyEvent
import android.view.accessibility.AccessibilityEvent
import android.view.MotionEvent
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Assert.assertNull
import org.junit.Assert.assertThrows
import org.junit.Test

/** The touch and key events the agent's gestures and typing send, timed on a fake clock. */
class InputTest {
    private val clock = FakeClock(10_000)
    private val injector = FakeInjector(density = 2f)
    private var field: FakeField? = null
    /** A field of the test's own, focused instead of [field] when set. */
    private var custom: Field? = null
    private val input = Input(injector, clock) { custom ?: field }

    private val actions get() = injector.touches.map { it.event.action }

    private val probe = FakeProbe()
    private val events = Events(probe, clock)

    /**
     * Issue #299: the app's window presents a frame every [everyMs] (a text field's caret blinks
     * every 500 ms) while [drawing]; a stalled app presents none. Frame stats count from here.
     */
    private fun frames(everyMs: Long = 500, drawing: () -> Boolean = { true }) {
        probe.activeWindow = 1
        var last = 0L
        probe.framePresented = {
            if (drawing()) last = clock.now / everyMs * everyMs
            last * 1_000_000
        }
        events.beginAction()
    }

    /**
     * Issue #299 review: the app's UI sends an accessibility event every [everyMs] from [from]
     * (its UI thread runs: a ticker, a semantics update), until [until].
     */
    private fun appLive(everyMs: Long = 100, from: Long = clock.now + everyMs, until: Long = clock.now + 10_000, pkg: String = "com.app") =
        clock.repeat(from, everyMs, until) { events.onEvent(UiEvent(AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED, pkg, null)) }

    private fun downs(code: Int) = injector.keys.count { it.event.code == code && it.event.action == KeyEvent.ACTION_DOWN }

    @Test
    fun swipeWaitsForInputWindowsBeforeSendingAndStampingDown() {
        var ready = false
        var dropped = false
        injector.onPrepareSwipe = { clock.advance(75); ready = true }
        injector.onTouch = { if (it.action == MotionEvent.ACTION_DOWN && !ready) dropped = true }
        input.swipe(0f, 100f, 0f, 500f, 400)
        assertFalse("DOWN was sent before the window could take it", dropped)
        assertEquals(10_075L, injector.touches.first().event.downAt)
        assertEquals(10_475L, injector.touches.last().event.at)
    }

    @Test
    fun checkedTypingRecognizesFocusMovingBetweenEmptyCodeBoxes() {
        field = FakeField()
        injector.onKey = { if (it.action == KeyEvent.ACTION_UP) field = FakeField() }
        assertTrue(input.typeChecked("abc", events)!!)
        assertEquals(10_000L, clock.now)
        assertEquals(3, injector.keys.count { it.event.action == KeyEvent.ACTION_UP })
    }

    @Test
    fun checkedTypingKeepsSurrogatePairsAndSelectionIntact() {
        field = FakeField("old", selectionStart = 0, selectionEnd = 3)
        assertTrue(input.typeChecked("😀好", events)!!)
        assertEquals("😀好", field!!.value)
        assertEquals(10_000L, clock.now)
    }

    @Test
    fun checkedTypingWithoutAnObservableFieldInjectsNothingForHostFallback() {
        assertNull(input.typeChecked("abc", events, waitForFocus = true))
        assertEquals(0, injector.keys.size)
        assertEquals(10_000L + Input.FOCUS_WAIT_MS, clock.now) // CI mode: waited for focus to arrive (issue #97)
    }

    /** Outside CI mode the host does not ask for the focus wait: no field, no wait, as before issue #97. */
    @Test
    fun checkedTypingOutsideCIDoesNotWaitForFocus() {
        assertNull(input.typeChecked("abc", events, waitForFocus = false))
        assertEquals(0, injector.keys.size)
        assertEquals(10_000L, clock.now)
    }

    @Test
    fun checkedTypingRemovesAnExactDuplicatedPrefixInTheOriginalNativeEditor() {
        val editor = FakeField(nativePlainText = true)
        field = editor
        injector.onKey = {
            if (it.action == KeyEvent.ACTION_UP) when (it.code) {
                KeyEvent.KEYCODE_B -> editor.value = "b"
                KeyEvent.KEYCODE_Y -> {
                    editor.value = "buddbuddy"
                    editor.selectionStart = editor.value.length
                    editor.selectionEnd = editor.value.length
                }
            }
        }
        assertTrue(input.typeChecked("buddy", events)!!)
        assertEquals("buddy", editor.value)
        assertEquals(5, editor.selectionStart)
    }

    @Test
    fun checkedTypingBoundsRetriesForMaskedOrFormattedFields() {
        field = FakeField("hidden")
        appLive() // issue #299: the app's UI runs (it sends events), so a key the field never shows was lost
        assertTrue(input.typeChecked("abc", events)!!)
        assertEquals(12_300L, clock.now) // issue #299: typed again a cap after the app's latest event (11 s), then a second for it
        assertEquals(listOf(KeyEvent.KEYCODE_A, KeyEvent.KEYCODE_A, KeyEvent.KEYCODE_B, KeyEvent.KEYCODE_C),
            injector.keys.filter { it.event.action == KeyEvent.ACTION_UP }.map { it.event.code })
    }

    /**
     * Issue #97: an Enter whose editor action moves focus to the next field returns once the
     * view has focus, before the keyboard's connection has moved there; a key typed meanwhile
     * is dropped and the field stays empty. A one-character text was typed unchecked, so
     * `inputText: "c"` reported success with nothing typed. The key must be checked and sent
     * again once the field takes keys. The connection moves 800 ms after the Enter, on the
     * fake clock.
     */
    @Test
    fun checkedTypingOfOneCharacterRetypesAKeyLostWhileFocusIsStillMoving() {
        val third = FakeField(nativePlainText = true)
        field = third
        var connected = false
        appLive() // issue #299: the app's UI runs meanwhile: the key was lost, not late
        clock.postDelayed({ connected = true }, 800)
        injector.onKey = { if (it.action == KeyEvent.ACTION_UP && connected) third.value += letter(it) }
        assertTrue(input.typeChecked("c", events)!!)
        assertEquals("c", third.value)
        assertEquals(2, injector.keys.count { it.event.action == KeyEvent.ACTION_UP })
    }

    /** Issue #97: when the focused editor never takes the text, typing fails and says so. */
    @Test
    fun checkedTypingFailsWhenTheFocusedEditorNeverTakesTheText() {
        field = FakeField(nativePlainText = true)
        appLive()
        val e = assertThrows(IllegalStateException::class.java) { input.typeChecked("c", events) }
        assertTrue(e.message, e.message!!.contains("no field took the text"))
    }

    /**
     * Issue #299: a first key is typed again only when it provably did not land: the app drew
     * frames for a while after the wait and the field still did not show it. An app that drew
     * nothing (stalled, or nothing to show it is running) gives no proof, and the key may only
     * be late: typing fails, saying so, and the key goes once.
     */
    @Test
    fun checkedTypingFailsRatherThanTypeAFirstKeyAgainWithNoProofItWasLost() {
        field = FakeField(nativePlainText = true)
        val e = assertThrows(IllegalStateException::class.java) { input.typeChecked("c", events) }
        assertTrue(e.message, e.message!!.contains("the field did not show the first key"))
        assertEquals(1, downs(KeyEvent.KEYCODE_C))
    }

    /**
     * Issue #299: the first key was typed again after 1 s with no echo, so a field 1.5 s behind
     * (a cold Flutter frame, a GC stall: the app draws nothing meanwhile) got it twice, and the
     * duplicate-prefix check fixes that only in a blank field. The app drew nothing before the
     * field showed the key: no proof it was lost, and the key goes once.
     */
    @Test
    fun checkedTypingIntoAFieldBehindOnItsFirstKeyTypesItOnce() {
        val f = FakeField("x ", nativePlainText = true)
        field = f
        val stallEnd = clock.now + 1500
        frames(16) { clock.now >= stallEnd }
        var handled = f.value
        injector.onKey = { k ->
            if (k.action == KeyEvent.ACTION_DOWN) {
                handled += letter(k)
                val shows = handled
                clock.postAt({ f.value = shows; f.selectionStart = shows.length; f.selectionEnd = shows.length },
                    maxOf(stallEnd, clock.now + 16))
            }
        }
        assertTrue(input.typeChecked("gamma", events)!!)
        clock.advance(100)
        assertEquals("x gamma", f.value)
        assertEquals(1, downs(KeyEvent.KEYCODE_G))
    }

    /** A field that shows each key a frame after its app handles it, handling keys from [stallEnd] on. */
    private fun stalledUntil(f: FakeField, stallEnd: Long, roundTripMs: Long = 16) {
        var handled = f.value
        injector.onKey = { k ->
            if (k.action == KeyEvent.ACTION_DOWN) {
                handled += letter(k)
                val shows = handled
                clock.postAt({ f.value = shows; f.selectionStart = shows.length; f.selectionEnd = shows.length },
                    maxOf(stallEnd, clock.now) + roundTripMs)
            }
        }
    }

    /**
     * Issue #299 follow-up (seen on a device under load: "fast" became "FAT"): in a field that
     * rewrites, typed one key at a time, a key shown after the cap counted as rejected, so the
     * next key went in at once, edited the value from before it, and the step passed without it.
     * Here "s" shows 400 ms late (cap 300 ms), and the app sends no event and draws nothing that
     * would show it is running meanwhile: the key is waited for, and nothing is lost.
     */
    @Test
    fun checkedTypingIntoARewritingFieldWaitsForAKeyShownAfterTheCap() {
        val f = RewritingField(clock, showAfter = { if (it == 's') 400L else 16L })
        field = f.field
        f.attach(injector)
        assertTrue(input.typeChecked("fast", events)!!)
        clock.advance(500)
        assertEquals("FAST", f.field.value)
        assertEquals("acknowledged", input.lastTyping)
        assertEquals(4, injector.keys.count { it.event.action == KeyEvent.ACTION_DOWN })
    }

    /**
     * Issue #299 follow-up (an external Compose form flow on a loaded emulator: the field never
     * showed the "s" of "runs", and the step passed): an upper-casing field drops one key outright,
     * once, after it has taken other letters. A letter is a kind of key it takes, so the key was
     * lost, not filtered: it is typed again once, confirmed, and the text ends exactly right.
     */
    @Test
    fun checkedTypingResendsOnceALetterAFieldThatTakesLettersDropped() {
        val f = RewritingField(clock, drops = { ch, n -> ch == 'k' && n == 1 })
        field = f.field
        f.attach(injector)
        appLive() // issue #299 review: the app's UI runs, so a key it never shows was lost
        assertTrue(input.typeChecked("quick brown fox", events)!!)
        clock.advance(100)
        assertEquals("QUICK BROWN FOX", f.field.value)
        assertEquals("repaired", input.lastTyping)
        assertEquals(2, downs(KeyEvent.KEYCODE_K))
        assertEquals(0, downs(KeyEvent.KEYCODE_DEL))
    }

    /** Issue #299 follow-up, the filter guard: a kind of key the field never took (a space) is its filter's: no resend. */
    @Test
    fun checkedTypingSendsAFilteredSpaceOnce() {
        val f = RewritingField(clock, transform = { it.uppercase().replace(" ", "") })
        field = f.field
        f.attach(injector)
        appLive() // the app runs: a space, a kind it never took, is still its filter's
        assertTrue(input.typeChecked("ab cd ef", events)!!)
        clock.advance(100)
        assertEquals("ABCDEF", f.field.value)
        assertEquals(2, downs(KeyEvent.KEYCODE_SPACE))
        assertEquals("acknowledged", input.lastTyping)
    }

    /**
     * Issue #299 follow-up: a letter the field drops again when typed again, followed by a key it
     * takes (a gap, then later keys: a real loss), fails the step, naming it.
     */
    @Test
    fun checkedTypingFailsWhenAResentKeyStillDoesNotShow() {
        val f = RewritingField(clock, drops = { ch, _ -> ch == 'k' })
        field = f.field
        f.attach(injector)
        appLive()
        val e = assertThrows(IllegalStateException::class.java) { input.typeChecked("quick brown fox", events) }
        assertTrue(e.message, e.message!!.contains("the field did not take \"k\""))
        assertEquals(2, downs(KeyEvent.KEYCODE_K))
        assertEquals(0, downs(KeyEvent.KEYCODE_B))
    }

    /**
     * An upper-casing field whose app stalls 1.5 s on "c" and then handles the keys in order,
     * each a frame after the one before. [frames] in the test say what else draws meanwhile.
     */
    private fun stallsOnC(f: FakeField) {
        var handled = ""
        var busy = clock.now
        var stalled = false
        injector.onKey = { k ->
            if (k.action == KeyEvent.ACTION_DOWN) {
                val ch = letter(k)
                handled += ch
                busy = maxOf(busy, clock.now) + if (ch == 'c' && !stalled) { stalled = true; 1500L } else 16L
                val shows = handled.uppercase()
                clock.postAt({ f.value = shows; f.selectionStart = shows.length; f.selectionEnd = shows.length }, busy)
            }
        }
    }

    /**
     * Issue #299 review: a key is sent again only on proof it was lost. An app stalled 1.5 s on
     * "c" sends nothing meanwhile; the wait's time bound is no proof. Sent again on it, the late
     * original showed first, the copy went in unwatched, and "abcd" became "ABCCD", reported
     * repaired. Now "c" goes once.
     */
    @Test
    fun checkedTypingNeverResendsAKeyOnTheTimeBoundAlone() {
        val f = FakeField(nativePlainText = true, selectionStart = 0, selectionEnd = 0)
        field = f
        stallsOnC(f)
        assertTrue(input.typeChecked("abcd", events)!!)
        clock.advance(2_000)
        assertEquals("ABCD", f.value)
        assertEquals(1, downs(KeyEvent.KEYCODE_C))
    }

    /** Issue #299 review: the same next to a spinner, whose frames say "running" while the UI thread is stalled: no resend. */
    @Test
    fun checkedTypingNeverResendsAKeyOnFramesAlone() {
        val f = FakeField(nativePlainText = true, selectionStart = 0, selectionEnd = 0)
        field = f
        frames(16)
        stallsOnC(f)
        assertTrue(input.typeChecked("abcd", events)!!)
        clock.advance(2_000)
        assertEquals("ABCD", f.value)
        assertEquals(1, downs(KeyEvent.KEYCODE_C))
    }

    /**
     * Issue #299 review: an upper-casing field at the max length its node reports ("abcdef" into
     * 4) is full: nothing more is sent, and the step passes with "ABCD", as on main.
     */
    @Test
    fun checkedTypingStopsAtTheMaxLengthTheNodeReports() {
        val f = RewritingField(clock, transform = { it.uppercase().take(4) })
        f.field.maxLength = 4
        field = f.field
        f.attach(injector)
        appLive()
        assertTrue(input.typeChecked("abcdef", events)!!)
        clock.advance(100)
        assertEquals("ABCD", f.field.value)
        assertEquals(0, downs(KeyEvent.KEYCODE_E))
    }

    /**
     * Issue #299 review: the same cap in the app's change handler (no max length on the node).
     * "e" never shows, is sent again on proof, still does not show; "f" goes once and does not
     * show either: the field is full, and the step passes with "ABCD".
     */
    @Test
    fun checkedTypingPassesAFieldCappedInItsChangeHandler() {
        val f = RewritingField(clock, transform = { it.uppercase().take(4) })
        field = f.field
        f.attach(injector)
        appLive()
        assertTrue(input.typeChecked("abcdef", events)!!)
        clock.advance(100)
        assertEquals("ABCD", f.field.value)
        assertEquals(2, downs(KeyEvent.KEYCODE_E))
        assertEquals(1, downs(KeyEvent.KEYCODE_F))
    }

    /**
     * Issue #299 follow-up: an app that stops responding while keys go in one at a time (its
     * field can no longer be read, and it sends nothing) fails the step, saying so, instead of
     * passing with what it took.
     */
    @Test
    fun checkedTypingFailsWhenTheAppStopsRespondingWhileTyping() {
        val f = RewritingField(clock)
        field = f.field
        f.attach(injector)
        val app = injector.onKey
        injector.onKey = { app(it); if (it.code == KeyEvent.KEYCODE_S && it.action == KeyEvent.ACTION_UP) field = null }
        val e = assertThrows(IllegalStateException::class.java) { input.typeChecked("fast", events) }
        assertTrue(e.message, e.message!!.contains("the app stopped responding while typing"))
        assertEquals(0, downs(KeyEvent.KEYCODE_T))
    }

    /**
     * Issue #299 follow-up, the filter guard: a field that rewrites and drops spaces, in an app
     * that draws after each key (a View or Compose app resets its caret), rejects a space for
     * about the cap: its app showed it was running, and the field did not show the key.
     */
    @Test
    fun checkedTypingIntoARewritingFilterOfALiveAppRejectsInAboutTheCap() {
        val f = RewritingField(clock, transform = { it.uppercase().replace(" ", "") })
        field = f.field
        f.attach(injector)
        frames(16)
        assertTrue(input.typeChecked("ab cd", events)!!)
        clock.advance(100)
        assertEquals("ABCD", f.field.value)
        assertTrue("took ${clock.now - 10_100} ms", clock.now - 10_100 <= Input.ACK_MS + 4 * 16 + 32)
    }

    /**
     * Issue #299 review: frames are no proof. An indeterminate spinner draws every frame on the
     * render thread while the app's UI thread is stalled 1.5 s: the frame rule took the key for
     * lost and typed it twice. The app's UI sends no event while stalled: the key goes once.
     */
    @Test
    fun checkedTypingNextToASpinnerInAStalledAppTypesTheFirstKeyOnce() {
        val f = FakeField("x ", nativePlainText = true)
        field = f
        frames(16) // the spinner: a frame every 16 ms, stall or not
        stalledUntil(f, clock.now + 1500)
        assertTrue(input.typeChecked("gamma", events)!!)
        clock.advance(100)
        assertEquals("x gamma", f.value)
        assertEquals(1, downs(KeyEvent.KEYCODE_G))
    }

    /**
     * Issue #299 review: an app whose frames cannot be read (a Flutter app draws into a
     * SurfaceView, which the window's frame stats do not count: on the device they showed none)
     * but whose UI sends events loses a first key while the keyboard still connects (issue #97):
     * the events prove its UI runs, and the key is typed again once.
     */
    @Test
    fun checkedTypingRetypesALostFirstKeyInAnAppWhoseFramesCannotBeRead() {
        val f = FakeField(nativePlainText = true, packageName = "com.app")
        field = f
        probe.activeWindow = -1 // no frame stats
        appLive(everyMs = 250)
        var connected = false
        clock.postDelayed({ connected = true }, 800)
        injector.onKey = { if (it.action == KeyEvent.ACTION_UP && connected) f.value += letter(it) }
        assertTrue(input.typeChecked("c", events)!!)
        assertEquals("c", f.value)
        assertEquals(2, downs(KeyEvent.KEYCODE_C))
    }

    /** Issue #299 review: events from another app (here the keyboard's own package) prove nothing of the field's app. */
    @Test
    fun checkedTypingIgnoresAnotherAppsEventsAsProof() {
        val f = FakeField(nativePlainText = true, packageName = "com.app")
        field = f
        appLive(pkg = "com.other")
        val e = assertThrows(IllegalStateException::class.java) { input.typeChecked("c", events) }
        assertTrue(e.message, e.message!!.contains("the field did not show the first key"))
        assertEquals(1, downs(KeyEvent.KEYCODE_C))
    }

    /**
     * Issue #299 review: a Flutter-like field (no frames to read) whose engine is stalled 1.5 s
     * sends nothing meanwhile; when it resumes its first semantics update comes before the key's
     * round trip ends (100 ms later). The cap after that first event covers it: the key goes once.
     */
    @Test
    fun checkedTypingIntoAStalledFlutterLikeFieldTypesTheFirstKeyOnce() {
        val f = FakeField("x ", nativePlainText = true, packageName = "com.app")
        field = f
        probe.activeWindow = -1
        val stallEnd = clock.now + 1500
        appLive(from = stallEnd) // the engine's semantics updates, once it runs again
        stalledUntil(f, stallEnd, roundTripMs = 100)
        assertTrue(input.typeChecked("gamma", events)!!)
        clock.advance(200)
        assertEquals("x gamma", f.value)
        assertEquals(1, downs(KeyEvent.KEYCODE_G))
    }

    /**
     * Issue #97: with no editor focused yet (focus still moving), typing in CI mode waits for one
     * first. Outside CI it does not wait: nothing is typed, and the host uses its own guard.
     */
    @Test
    fun checkedTypingWaitsForAnEditorToTakeFocusBeforeSendingKeys() {
        val third = FakeField(nativePlainText = true)
        clock.postDelayed({ field = third }, 300)
        injector.onKey = { if (it.action == KeyEvent.ACTION_UP && field === third) third.value += letter(it) }
        assertTrue(input.typeChecked("c", events, waitForFocus = true)!!)
        assertEquals("c", third.value)
        assertEquals(10_300L, injector.keys.first().event.at)
    }

    @Test
    fun checkedTypingOutsideCIHandsAFieldNotFocusedYetToTheHost() {
        val third = FakeField(nativePlainText = true)
        clock.postDelayed({ field = third }, 300)
        assertNull(input.typeChecked("c", events, waitForFocus = false))
        assertEquals(0, injector.keys.size)
        assertEquals(10_000L, clock.now)
    }

    /**
     * Issue #97: a plain field whose filter rejects the first character but takes the rest
     * (a digits-only field, a filter that drops "+") must not fail the step: only a field that
     * took nothing of the text does.
     */
    @Test
    fun checkedTypingIntoAFieldThatRejectsOnlyTheFirstCharacterSucceeds() {
        val filtered = FakeField(nativePlainText = true)
        field = filtered
        appLive()
        injector.onKey = { if (it.action == KeyEvent.ACTION_UP && it.code != KeyEvent.KEYCODE_X) filtered.value += letter(it) }
        assertTrue(input.typeChecked("xab", events)!!)
        assertEquals("ab", filtered.value)
    }

    private fun letter(k: Key) = 'a' + (k.code - KeyEvent.KEYCODE_A)

    /**
     * Issue #279: a field whose app upper-cases every value restarts its input on each key, and
     * a key sent before that restart is through edits the value from before the key ahead of
     * it. Keys sent in chunks lost most of the text ("QX" for "quick brown fox") and the step
     * passed. The field showed "Q" for "q": it rewrites what is typed, so each key goes in once
     * the field shows the one before, and the whole text arrives.
     */
    @Test
    fun checkedTypingIntoAFieldThatRewritesItsValueLosesNoKeys() {
        val upper = RewritingField(clock)
        field = upper.field
        upper.attach(injector)
        assertTrue(input.typeChecked("quick brown fox", events)!!)
        clock.advance(100) // the app's frames for the last keys
        assertEquals("QUICK BROWN FOX", upper.field.value)
        assertEquals("acknowledged", input.lastTyping)
        assertEquals(15, injector.keys.count { it.event.action == KeyEvent.ACTION_DOWN })
    }

    /**
     * Issue #279, as #297's review settled it: a plain field in a slow app can lose keys too,
     * but it shows the first key as typed, so its keys go in chunks, and a chunk's text gets no
     * verdict: a field only behind looks the same (issue #297). It is not repaired: no DEL.
     */
    @Test
    fun checkedTypingLeavesAPlainFieldThatLostKeysInChunksAlone() {
        val slow = RewritingField(clock, transform = { it })
        field = slow.field
        slow.attach(injector)
        assertTrue(input.typeChecked("quick brown fox", events)!!)
        clock.advance(100)
        assertEquals("qx", slow.field.value)
        assertEquals(0, injector.keys.count { it.event.code == KeyEvent.KEYCODE_DEL })
        assertNull(input.lastTyping)
    }

    /**
     * Issue #279: a field that loses a key whatever the pace (here every "c": the field shows
     * it, then the next key edits the value from before it) fails the step instead of passing
     * with the key missing. Since #297's review the repair stops at the first key that does not
     * add itself (the "k" after the "c" typed again shows "QUIK"), naming what the field holds.
     */
    @Test
    fun checkedTypingFailsWhenTheFieldLosesAKeyForGood() {
        val lossy = RewritingField(clock, lagging = { it == 'c' })
        field = lossy.field
        lossy.attach(injector)
        val e = assertThrows(IllegalStateException::class.java) { input.typeChecked("quick brown fox", events) }
        assertTrue(e.message, e.message!!.contains("the field holds \"QUIK\", expected \"quick brown fox\""))
        assertTrue(e.message, e.message!!.contains("the repair stopped there"))
    }

    /**
     * Issue #279: a field that shows each key as typed, at once, keeps the fast path: the keys
     * go in chunks (only the first key and the last waited for), and nothing waits on the clock.
     */
    @Test
    fun checkedTypingIntoAFieldThatEchoesKeysKeepsTheChunkedPath() {
        val plain = FakeField(nativePlainText = true)
        field = plain
        injector.onKey = {
            if (it.action == KeyEvent.ACTION_UP) {
                plain.value += if (it.code == KeyEvent.KEYCODE_SPACE) ' ' else letter(it)
                plain.selectionStart = plain.value.length
                plain.selectionEnd = plain.value.length
            }
        }
        assertTrue(input.typeChecked("quick brown fox", events)!!)
        assertEquals("quick brown fox", plain.value)
        assertEquals(10_000L, clock.now)
        assertNull(input.lastTyping)
        val ups = injector.keys.filter { it.event.action == KeyEvent.ACTION_UP }
        assertEquals(listOf(0, 14), ups.withIndex().filter { it.value.sync }.map { it.index })
    }

    /**
     * Issue #279: a field whose app clears it once it is full (a code field checking a wrong
     * code) does not hold the text at the end, and has lost no key. Its keys go in chunks:
     * no verdict, nothing typed again, and no wait at all (issue #297 review).
     */
    @Test
    fun checkedTypingIntoAFieldThatClearsItselfTypesNothingAgain() {
        val code = FakeField(nativePlainText = true)
        field = code
        injector.onKey = {
            if (it.action == KeyEvent.ACTION_UP) {
                code.value += letter(it)
                if (code.value.length == 6) code.value = ""
                code.selectionStart = code.value.length
                code.selectionEnd = code.value.length
            }
        }
        assertTrue(input.typeChecked("abcdef", events)!!)
        assertEquals("", code.value)
        assertEquals(6, injector.keys.count { it.event.action == KeyEvent.ACTION_UP })
        assertEquals(10_000L, clock.now)
    }

    /** Issue #279 review: a field at its max length keeps the start of the text: no verdict, no DEL, no wait. */
    @Test
    fun checkedTypingIntoAFieldAtItsMaxLengthWaitsOnlyForTheTextToStop() {
        val short = FakeField(nativePlainText = true)
        field = short
        injector.onKey = {
            if (it.action == KeyEvent.ACTION_UP && short.value.length < 4) {
                short.value += letter(it)
                short.selectionStart = short.value.length
                short.selectionEnd = short.value.length
            }
        }
        assertTrue(input.typeChecked("abcdefgh", events)!!)
        assertEquals("abcd", short.value)
        assertEquals(0, injector.keys.count { it.event.code == KeyEvent.KEYCODE_DEL })
        assertEquals(10_000L, clock.now)
    }

    /**
     * Issue #279: a filter's rejections are the app's own (a digits-only field, a field that
     * drops spaces): a key the field showed no change for, typed one at a time, is not missing.
     * Upper-cased and with its spaces dropped, "ab cd" is "ABCD", and the step passes.
     */
    @Test
    fun checkedTypingIntoARewritingFieldThatRejectsSomeKeysPasses() {
        val noSpaces = RewritingField(clock, transform = { it.uppercase().replace(" ", "") })
        field = noSpaces.field
        noSpaces.attach(injector)
        assertTrue(input.typeChecked("ab cd", events)!!)
        clock.advance(100)
        assertEquals("ABCD", noSpaces.field.value)
    }

    /**
     * Issue #279 review: a field that echoes keys and filters some out (here spaces) ends with
     * a subsequence of the text, its last key there, and has lost no key: it never shows a
     * space. That is the app's own: no DEL is sent to it, nothing is typed again, nothing waits.
     */
    @Test
    fun checkedTypingIntoAnEchoingFieldThatFiltersSendsNoDelAndDoesNotWait() {
        val noSpaces = CaretField(filter = { it != ' ' })
        custom = noSpaces
        injector.onKey = noSpaces::key
        assertTrue(input.typeChecked("ab cd ef gh", events)!!)
        assertEquals("abcdefgh", noSpaces.value)
        assertEquals(0, injector.keys.count { it.event.code == KeyEvent.KEYCODE_DEL })
        assertEquals(10_000L, clock.now)
        assertNull(input.lastTyping)
    }

    /**
     * Issue #297 review: a first key with no case (a digit) shows as typed in a field that
     * upper-cases letters ("42 quick brown foxes"). The keys go one at a time until one shows
     * what the field does: here "q" shows as "Q", so the rest goes one at a time too, and the
     * field loses no key (it used to lose them in chunks and be repaired).
     */
    @Test
    fun checkedTypingFindsAFieldThatRewritesLettersBehindAFirstDigit() {
        val f = CaretField(transform = { it.uppercase() })
        custom = f
        injector.onKey = f::key
        assertTrue(input.typeChecked("ab", events)!!) // warm: a letter first is judged at once
        assertEquals("acknowledged", input.lastTyping)
        val g = CaretField(transform = { it.uppercase() }, digits = true)
        custom = g
        injector.onKey = g::key
        assertTrue(input.typeChecked("4 ab", events)!!)
        assertEquals("4 AB", g.value)
        assertEquals("acknowledged", input.lastTyping)
    }

    /** Issue #297 review: digits that show as typed, followed by no letter, go in chunks: no wait. */
    @Test
    fun checkedTypingOfDigitsIntoAnEchoingFieldStaysInChunks() {
        val g = CaretField(digits = true)
        custom = g
        injector.onKey = g::key
        assertTrue(input.typeChecked("2468", events)!!)
        assertEquals("2468", g.value)
        assertEquals(10_000L, clock.now)
        assertNull(input.lastTyping)
    }

    /** A US phone number formatter: "(555) 123-4567" from its digits, as typed so far. */
    private fun usPhone(s: String): String {
        val d = s.filter { it.isDigit() }.take(10)
        return when {
            d.length <= 3 -> d
            d.length <= 6 -> "(${d.take(3)}) ${d.drop(3)}"
            else -> "(${d.take(3)}) ${d.substring(3, 6)}-${d.drop(6)}"
        }
    }

    /**
     * Issue #299: a phone field whose app formats each value (and so restarts its input on each
     * key) shows its first digits as typed and rewrites from the fourth. The text has no letter,
     * so the keys after the first went in one chunk, and the field kept "57" of "5551234567".
     * A phone field (its input type) is followed one key at a time until a rewrite shows; from
     * there it is typed one key at a time, and nothing is lost.
     */
    @Test
    fun checkedTypingIntoAPhoneFormatterThatRewritesPartwayLosesNoKeys() {
        val phone = RewritingField(clock, transform = ::usPhone, inputType = InputType.TYPE_CLASS_PHONE)
        field = phone.field
        phone.attach(injector)
        assertTrue(input.typeChecked("5551234567", events)!!)
        clock.advance(100)
        assertEquals("(555) 123-4567", phone.field.value)
        assertEquals("acknowledged", input.lastTyping)
        assertEquals(10, injector.keys.count { it.event.action == KeyEvent.ACTION_DOWN })
        // Each key alone, once the field showed the one before: every DOWN-UP pair waited for.
        assertTrue(injector.keys.filter { it.event.action == KeyEvent.ACTION_UP }.all { it.sync })
    }

    /** Issue #299: the same formatter whose node does not say its input type, the keyboard's editor settings do. */
    @Test
    fun checkedTypingFindsAPhoneFieldByItsEditorSettings() {
        val phone = RewritingField(clock, transform = ::usPhone)
        phone.field.packageName = "com.app"
        val typed = Input(injector, clock, { EditorSettings("com.app", InputType.TYPE_CLASS_PHONE, 0) }) { phone.field }
        phone.attach(injector)
        assertTrue(typed.typeChecked("5551234567", events)!!)
        clock.advance(100)
        assertEquals("(555) 123-4567", phone.field.value)
    }

    /**
     * Issue #299 review: editor settings are trusted only when they are known to be the field's
     * app's: with either package unknown they decide nothing, and the digits go in chunks.
     */
    @Test
    fun checkedTypingTrustsNoEditorSettingsOfAnUnknownApp() {
        for ((fieldPkg, settingsPkg) in listOf(null to "com.app", "com.app" to null, "com.app" to "com.other")) {
            injector.keys.clear()
            val phone = RewritingField(clock, transform = ::usPhone)
            phone.field.packageName = fieldPkg
            val typed = Input(injector, clock, { EditorSettings(settingsPkg, InputType.TYPE_CLASS_PHONE, 0) }) { phone.field }
            phone.attach(injector)
            assertTrue(typed.typeChecked("5551234567", events)!!)
            assertNull("$fieldPkg / $settingsPkg", typed.lastTyping)
            assertEquals("$fieldPkg / $settingsPkg", 2, injector.keys.count { it.event.action == KeyEvent.ACTION_UP && it.sync })
            clock.advance(100)
        }
    }

    /** Issue #299: a number field that shows its digits as typed loses nothing and waits for nothing: no verdict. */
    @Test
    fun checkedTypingIntoAnEchoingNumberFieldAddsNoWait() {
        val n = FakeField(nativePlainText = true, inputType = InputType.TYPE_CLASS_NUMBER)
        field = n
        injector.onKey = {
            if (it.action == KeyEvent.ACTION_UP) {
                n.value += '0' + (it.code - KeyEvent.KEYCODE_0)
                n.selectionStart = n.value.length
                n.selectionEnd = n.value.length
            }
        }
        assertTrue(input.typeChecked("2468", events)!!)
        assertEquals("2468", n.value)
        assertEquals(10_000L, clock.now)
        assertNull(input.lastTyping)
    }

    /**
     * Issue #299: the probe after a first key with no case paid the cap for each key a filter
     * dropped: "1 2 3 4 a" into a field that strips spaces took 4 caps (1.2 s; 4 s in CI). A
     * filter is no rewriter of letters: the probe stops at the first key filtered, one cap, and
     * the rest goes in chunks.
     */
    @Test
    fun checkedTypingProbeStopsAtTheFirstKeyAFilterDrops() {
        val noSpaces = CaretField(filter = { it != ' ' }, digits = true)
        custom = noSpaces
        injector.onKey = noSpaces::key
        assertTrue(input.typeChecked("1 2 3 4 a", events)!!)
        assertEquals("1234a", noSpaces.value)
        assertEquals(10_000L + Input.ACK_MS, clock.now)
        assertNull(input.lastTyping)
    }

    /** Issue #299: the same in CI mode, where the cap is longer: one cap. */
    @Test
    fun checkedTypingProbeStopsAtTheFirstKeyAFilterDropsInCI() {
        val noSpaces = CaretField(filter = { it != ' ' }, digits = true)
        custom = noSpaces
        injector.onKey = noSpaces::key
        assertTrue(input.typeChecked("1 2 3 4 a", events, ci = true)!!)
        assertEquals("1234a", noSpaces.value)
        assertEquals(10_000L + Input.ACK_CI_MS, clock.now)
    }

    /**
     * Issue #279 review: the repair stops typing as soon as the focus leaves the field, and the
     * step fails (the field was changed and cannot be checked): no key goes to the field that
     * took the focus.
     */
    @Test
    fun checkedTypingRepairStopsWhenTheFocusMoves() {
        val lossy = RewritingField(clock, lagging = { it == 'c' })
        field = lossy.field
        lossy.attach(injector)
        val other = FakeField(nativePlainText = true)
        val app = injector.onKey
        injector.onKey = {
            app(it)
            if (it.code == KeyEvent.KEYCODE_DEL && it.action == KeyEvent.ACTION_UP) field = other
        }
        val e = assertThrows(IllegalStateException::class.java) { input.typeChecked("quick brown fox", events) }
        assertTrue(e.message, e.message!!.contains("the focus left the field"))
        val afterDel = injector.keys.indexOfLast { it.event.code == KeyEvent.KEYCODE_DEL }
        assertTrue(afterDel >= 0)
        assertEquals(emptyList<Int>(), injector.keys.drop(afterDel + 1).map { it.event.code })
        assertEquals("", other.value)
    }

    /**
     * Issue #279 review: a DEL deletes a whole grapheme (a skin-toned emoji is two code points).
     * Counting DELs by code point deleted a key that was right; the repair reads the field after
     * each DEL instead, and the text comes out whole. The field upper-cases (keys one at a time)
     * and loses "c" once to the key after it.
     */
    @Test
    fun checkedTypingRepairDeletesAnEmojiInTheTailWithOneDel() {
        val f = CaretField(lose = 'c', transform = { it.uppercase() })
        custom = f
        injector.onKey = f::key
        assertTrue(input.typeChecked("abcd👍🏽e", events)!!)
        assertEquals("ABCD👍🏽E", f.value)
        assertEquals("repaired", input.lastTyping)
        assertEquals(3, injector.keys.count { it.event.code == KeyEvent.KEYCODE_DEL && it.event.action == KeyEvent.ACTION_DOWN })
    }

    /**
     * Issue #279 review: a key typed again that the field shows late (50 ms, inside the cap) is
     * waited for, and the repair goes on.
     */
    @Test
    fun checkedTypingRepairWaitsForAKeyTypedAgainThatShowsLate() {
        val f = CaretField(lose = 'b', transform = { it.uppercase() }, lateOnRetype = 50, clock = clock)
        custom = f
        injector.onKey = f::key
        assertTrue(input.typeChecked("abc", events)!!)
        assertEquals("ABC", f.value)
        assertEquals("repaired", input.lastTyping)
    }

    /**
     * Issue #297: a field that loses no key but shows each one 3 to 5 frames late (a Flutter
     * field) showed "ga" of "gamma" for longer than two frames at the end of typing. That read
     * was taken for lost keys, and the repair's DELs and keys went in on top of the real text:
     * "gammamma". Its keys go in chunks, which get no verdict: no DEL, nothing typed again.
     */
    @Test
    fun checkedTypingIntoAFieldThatIsOnlyBehindRepairsNothing() {
        val late = LaggingField(clock)
        field = late.field
        late.attach(injector)
        assertTrue(input.typeChecked("gamma", events)!!)
        clock.advance(1_000)
        assertEquals("gamma", late.field.value)
        assertEquals(0, injector.keys.count { it.event.code == KeyEvent.KEYCODE_DEL })
        assertEquals(5, injector.keys.count { it.event.action == KeyEvent.ACTION_DOWN })
        assertNull(input.lastTyping)
    }

    /**
     * Issue #297 review: the same with a stall longer than the cap (a 400 ms frame on the third
     * key): "ga" stays for the whole cap, which made the loss look final, and the repair typed
     * "mma" again without a DEL. No verdict on chunks: the field ends with "gamma".
     */
    @Test
    fun checkedTypingIntoAFieldThatStallsLongerThanTheCapRepairsNothing() {
        val late = LaggingField(clock, lagFrames = listOf(4, 3, 25, 3, 4))
        field = late.field
        late.attach(injector)
        assertTrue(input.typeChecked("gamma", events)!!)
        clock.advance(2_000)
        assertEquals("gamma", late.field.value)
        assertEquals(0, injector.keys.count { it.event.code == KeyEvent.KEYCODE_DEL })
        assertEquals(5, injector.keys.count { it.event.action == KeyEvent.ACTION_DOWN })
    }

    /** Issue #297 review: a field that shows keys late and in bursts (several at once): no repair. */
    @Test
    fun checkedTypingIntoAFieldThatShowsKeysInBurstsRepairsNothing() {
        val f = FakeField(nativePlainText = true, selectionStart = 0, selectionEnd = 0)
        field = f
        var handled = ""
        injector.onKey = { k ->
            if (k.action == KeyEvent.ACTION_DOWN) {
                handled += letter(k)
                val shows = handled
                // Shown on the next 100 ms tick after 3 frames: keys close together show at once.
                val at = ((clock.now + 48) / 100 + 1) * 100
                clock.postAt({ f.value = shows; f.selectionStart = shows.length; f.selectionEnd = shows.length }, at)
            }
        }
        assertTrue(input.typeChecked("gamma", events)!!)
        clock.advance(1_000)
        assertEquals("gamma", f.value)
        assertEquals(0, injector.keys.count { it.event.code == KeyEvent.KEYCODE_DEL })
    }

    /** Issue #297 review: a field that shows keys late, as typed, then rewrites them all (upper case): no repair. */
    @Test
    fun checkedTypingIntoAFieldThatEchoesLateThenRewritesRepairsNothing() {
        val late = LaggingField(clock)
        field = late.field
        late.attach(injector)
        clock.postDelayed({ late.field.value = late.field.value.uppercase() }, 600)
        assertTrue(input.typeChecked("gamma", events)!!)
        clock.advance(1_000)
        assertEquals("GAMMA", late.field.value)
        assertEquals(0, injector.keys.count { it.event.code == KeyEvent.KEYCODE_DEL })
        assertEquals(5, injector.keys.count { it.event.action == KeyEvent.ACTION_DOWN })
    }

    /**
     * Issue #297 review: a DEL of the repair the field does not show within the cap leaves the
     * text read last stale, and a next DEL could delete what is right: the repair stops at that
     * DEL, and the step fails, saying so.
     */
    @Test
    fun checkedTypingRepairStopsAtADelTheFieldDoesNotShow() {
        val lossy = RewritingField(clock, lagging = { it == 'c' }, takesDel = false)
        field = lossy.field
        lossy.attach(injector)
        val e = assertThrows(IllegalStateException::class.java) { input.typeChecked("quick brown fox", events) }
        assertTrue(e.message, e.message!!.contains("a DEL to repair it did not show"))
        assertEquals(1, injector.keys.count { it.event.code == KeyEvent.KEYCODE_DEL && it.event.action == KeyEvent.ACTION_DOWN })
    }

    /**
     * Issue #297 review: a key typed again in a repair must add itself and nothing else. Here
     * the key lost was only late, and shows with the key typed again: the field jumps to more
     * than that key. The repair stops there (no key more), and the step fails, saying so.
     */
    @Test
    fun checkedTypingRepairStopsWhenAKeyTypedAgainShowsMore() {
        val f = CaretField(lose = 'b', transform = { it.uppercase() }, jumpOnRetype = "B")
        custom = f
        injector.onKey = f::key
        val e = assertThrows(IllegalStateException::class.java) { input.typeChecked("abc", events) }
        assertTrue(e.message, e.message!!.contains("more than that key"))
        val retyped = injector.keys.indexOfLast { it.event.code == KeyEvent.KEYCODE_B }
        assertEquals(emptyList<Int>(), injector.keys.drop(retyped + 1).map { it.event.code }.filter { it != KeyEvent.KEYCODE_B })
    }

    /**
     * Issue #297: the text the loss was judged on must be the text the repair deletes from.
     * Here the field (upper-casing: keys one at a time) loses "b" to "c" and shows it again once
     * the cursor is put after the text (it was only behind): the repair sees the text changed
     * before its first DEL, sends none, and judges again: the text is all there.
     */
    @Test
    fun checkedTypingRepairAbortsWhenTheTextChangesBeforeTheFirstDel() {
        val f = CaretField(lose = 'b', transform = { it.uppercase() }, reportsCaret = false, catchUpOnSelect = "ABC")
        custom = f
        injector.onKey = f::key
        assertTrue(input.typeChecked("abc", events)!!)
        assertEquals("ABC", f.value)
        assertEquals(0, injector.keys.count { it.event.code == KeyEvent.KEYCODE_DEL })
    }

    /** Issue #279 review: a caret mid-text, text after it: the repair deletes and types at the caret only. */
    @Test
    fun checkedTypingRepairsAtACaretWithTextAfterIt() {
        val f = CaretField("XY", caret = 1, lose = 'b', transform = { it.uppercase() })
        custom = f
        injector.onKey = f::key
        assertTrue(input.typeChecked("abc", events)!!)
        assertEquals("XABCY", f.value)
        assertEquals("repaired", input.lastTyping)
    }

    /**
     * A plain field editing at its caret (issue #279 review): keys go in at the caret, a DEL
     * deletes the grapheme before it (an emoji with its skin tone whole), text set replaces it
     * all; [transform] is applied to every value (upper-casing). [filter] keeps the characters
     * the field takes; [digits] maps the keys 0-9 too. [lose] is shown, then lost once to the
     * key after it (that key edits the value from before it). When the lost key is typed again:
     * [lateOnRetype] shows it that many ms late (on [clock]); [jumpOnRetype] shows that much more
     * with it. [reportsCaret] false reports the caret at 0; [catchUpOnSelect] is the value shown
     * once the caret is set (the lost key was only late).
     */
    private class CaretField(
        var value: String = "",
        var caret: Int = value.length,
        var lose: Char? = null,
        val filter: (Char) -> Boolean = { true },
        val transform: (String) -> String = { it },
        val digits: Boolean = false,
        val lateOnRetype: Long = 0,
        val clock: FakeClock? = null,
        var jumpOnRetype: String? = null,
        val reportsCaret: Boolean = true,
        var catchUpOnSelect: String? = null,
    ) : Field {
        override val editable = true
        override val showingHint = false
        override val nativePlainText = true
        override val text: CharSequence get() = value
        override val selectionStart get() = if (reportsCaret) caret else 0
        override val selectionEnd get() = if (reportsCaret) caret else 0
        private var stale: Pair<String, Int>? = null
        private var lost: Char? = null

        override fun setText(text: CharSequence): Boolean {
            value = transform(text.toString().filter(filter))
            return true
        }

        override fun select(start: Int, end: Int): Boolean {
            catchUpOnSelect?.let { value = it; caret = it.length; catchUpOnSelect = null; stale = null; return true }
            caret = end.coerceIn(0, value.length)
            return true
        }

        fun key(k: Key) {
            if (k.action != KeyEvent.ACTION_DOWN) return
            stale?.let { (v, c) -> value = v; caret = c; stale = null } // the key edits the value from before the lost one
            if (k.code == KeyEvent.KEYCODE_DEL) {
                if (caret == 0) return
                var i = value.offsetByCodePoints(caret, -1)
                if (value.codePointAt(i) in 0x1F3FB..0x1F3FF && i > 0) i = value.offsetByCodePoints(i, -1)
                value = value.removeRange(i, caret)
                caret = i
                return
            }
            val ch = when (k.code) {
                in KeyEvent.KEYCODE_A..KeyEvent.KEYCODE_Z -> 'a' + (k.code - KeyEvent.KEYCODE_A)
                in KeyEvent.KEYCODE_0..KeyEvent.KEYCODE_9 -> if (digits) '0' + (k.code - KeyEvent.KEYCODE_0) else return
                KeyEvent.KEYCODE_SPACE -> ' '
                else -> return
            }
            if (!filter(ch)) return
            val before = value to caret
            val retype = ch == lost
            val add = ch.toString() + (if (retype) jumpOnRetype.orEmpty().also { jumpOnRetype = null } else "")
            val next = transform(value.substring(0, caret) + add + value.substring(caret))
            val nextCaret = caret + add.length
            if (retype && lateOnRetype > 0 && clock != null) {
                lost = null
                clock.postDelayed({ value = next; caret = nextCaret }, lateOnRetype)
                return
            }
            value = next
            caret = nextCaret
            if (ch == lose) { stale = before; lost = ch; lose = null }
            else if (retype) lost = null
        }
    }

    @Test
    fun aTapIsDownThenUpWaitedFor() {
        input.tap(100f, 200f)
        assertEquals(listOf(MotionEvent.ACTION_DOWN, MotionEvent.ACTION_UP), actions)
        assertEquals(listOf(false, true), injector.touches.map { it.sync })
        assertTrue(injector.touches.all { it.event.downAt == 10_000L && it.event.x == 100f && it.event.y == 200f })
    }

    @Test
    fun aDoubleTapFitsTheDoubleTapTimeout() {
        input.doubleTap(5f, 5f)
        val downs = injector.touches.filter { it.event.action == MotionEvent.ACTION_DOWN }.map { it.event.at }
        assertEquals(2, downs.size)
        val gap = downs[1] - downs[0]
        assertTrue("gap $gap ms", gap in 40..300)
    }

    @Test
    fun doubleTapReachesControlsThatRequireContactAcrossFrames() {
        input.doubleTap(5f, 5f)
        assertEquals(listOf(
            MotionEvent.ACTION_DOWN, MotionEvent.ACTION_UP,
            MotionEvent.ACTION_DOWN, MotionEvent.ACTION_UP,
        ), actions)
        val events = injector.touches.map { it.event }
        val holds = events.chunked(2).map { (down, up) -> up.at - down.at }
        assertTrue("each contact must reach the reaction control: $holds", holds.all { it >= 75 })
    }

    @Test
    fun aPressHoldsForItsTime() {
        input.press(5f, 5f, 1_500)
        val (down, up) = injector.touches.map { it.event }
        assertEquals(1_500L, up.at - down.at)
        assertEquals(down.downAt, up.downAt)
    }

    @Test
    fun aSwipeMovesEveryFrameOverItsDuration() {
        input.swipe(0f, 1000f, 0f, 200f, 400)
        val moves = injector.touches.filter { it.event.action == MotionEvent.ACTION_MOVE }
        assertEquals((400 / Input.FRAME_MS).toInt(), moves.size)
        assertEquals(MotionEvent.ACTION_DOWN, actions.first())
        val up = injector.touches.last()
        assertEquals(MotionEvent.ACTION_UP, up.event.action)
        assertTrue(up.sync)
        assertEquals(200f, up.event.y)
        // Paced like a finger: the last move lands at the swipe's end.
        assertEquals(400L, moves.last().event.at - injector.touches.first().event.at)
        // Monotonic, evenly spread.
        val ys = moves.map { it.event.y }
        assertEquals(ys.sortedDescending(), ys)
    }

    @Test
    fun anUpOrDownDragEndsInACancelSoNothingFlings() {
        input.drag(500f, 1500f, 500f, 500f)
        val events = injector.touches.map { it.event }
        assertEquals(MotionEvent.ACTION_DOWN, events.first().action)
        assertEquals(MotionEvent.ACTION_CANCEL, events.last().action)
        // Stamped as a drag that ended a moment ago: DOWN back-dated, the rest in order.
        assertEquals(clock.now - 25 - (Input.DRAG_MS + Input.DRAG_REST_MS), events.first().at)
        val times = events.dropLast(1).map { it.at }
        assertEquals(times.sorted(), times)
        // At rest at the end: the last two moves at the same point.
        val moves = events.filter { it.action == MotionEvent.ACTION_MOVE }
        assertEquals(moves[moves.size - 2].y, moves.last().y)
        assertEquals(500f, moves.last().y)
        // The two first moves pass the touch slop (16 and 20 dp, at 2 px per dp), each waited for.
        assertEquals(1500f - 32f, moves[0].y)
        assertEquals(1500f - 40f, moves[1].y)
        assertTrue(injector.touches.filter { it.event.action == MotionEvent.ACTION_MOVE }.take(2).all { it.sync })
    }

    @Test
    fun aSidewaysDragLiftsSoAPagerSnaps() {
        input.drag(900f, 1000f, 100f, 1000f)
        assertEquals(MotionEvent.ACTION_UP, actions.last())
    }

    @Test
    fun aDragNeverStartsBeforeTheLastEvent() {
        input.tap(1f, 1f)
        val tappedAt = clock.now
        input.drag(500f, 1500f, 500f, 500f)
        val down = injector.touches.first { it.event.action == MotionEvent.ACTION_DOWN && it.event.downAt != tappedAt }
        assertTrue("drag DOWN at ${down.event.at}, tap at $tappedAt", down.event.at > tappedAt)
    }

    /** Issue #144 review: a double tap's back-dated first DOWN must not predate the last event. */
    @Test
    fun aDoubleTapNeverStartsBeforeTheLastEvent() {
        input.tap(1f, 1f)
        val tapUp = injector.touches.last().event.at
        input.doubleTap(5f, 5f)
        val down1 = injector.touches[2].event
        assertEquals(MotionEvent.ACTION_DOWN, down1.action)
        assertTrue("double tap DOWN at ${down1.at}, the tap's UP at $tapUp", down1.at > tapUp)
    }

    @Test
    fun aDragTooShortToMoveIsATap() {
        input.drag(10f, 10f, 10.5f, 10f)
        assertEquals(listOf(MotionEvent.ACTION_DOWN, MotionEvent.ACTION_UP), actions)
    }

    @Test
    fun aKeyIsDownThenUp() {
        input.key(KeyEvent.KEYCODE_ENTER)
        assertEquals(listOf(KeyEvent.ACTION_DOWN, KeyEvent.ACTION_UP), injector.keys.map { it.event.action })
        assertEquals(listOf(false, true), injector.keys.map { it.sync })
    }

    // AND-006: an IME can consume hardware Enter to dismiss itself without
    // submitting. The editor's advertised action must be delivered once instead.
    @Test
    fun enterUsesTheEditorsSubmitActionWithoutInjectingAnotherKey() {
        var submissions = 0
        val editor = object : Field by FakeField(value = "hello") {
            override fun submitIme(): Boolean { submissions++; return true }
        }
        val input = Input(injector, clock) { editor }
        input.key(KeyEvent.KEYCODE_ENTER)
        assertEquals(1, submissions)
        assertTrue("hardware Enter can be consumed or submit twice", injector.keys.isEmpty())
    }

    @Test
    fun unsupportedSubmitAndLongEnterKeepHardwareKeySemantics() {
        var attempts = 0
        val editor = object : Field by FakeField(value = "hello") {
            override fun submitIme(): Boolean { attempts++; return false }
        }
        val input = Input(injector, clock) { editor }
        input.key(KeyEvent.KEYCODE_ENTER)
        assertEquals(1, attempts)
        assertEquals(listOf(KeyEvent.ACTION_DOWN, KeyEvent.ACTION_UP), injector.keys.map { it.event.action })
        input.key(KeyEvent.KEYCODE_ENTER, longPress = true)
        assertEquals("long presses must not turn into editor actions", 1, attempts)
        assertEquals(5, injector.keys.size)
    }

    // Issue #32: a multi-line Compose field advertises the IME action and does not report
    // isMultiLine, so only its keyboard settings tell Enter to type a new line. Performing the
    // action there typed nothing.
    private fun advertising(pkg: String = "dev.app", onSubmit: () -> Unit) = object : Field by FakeField(value = "one") {
        override fun submitIme(): Boolean { onSubmit(); return true }
        override val packageName: CharSequence? get() = pkg
    }

    @Test
    fun enterInAMultiLineEditorTypesANewLineInsteadOfTheEditorAction() {
        var submissions = 0
        var reads = 0
        val editor = advertising { submissions++ }
        val input = Input(injector, clock, { reads++; EditorSettings("dev.app", 0x28001, 0x42000000) }) { editor }
        assertEquals("hardware", input.key(KeyEvent.KEYCODE_ENTER))
        assertEquals("the IME action types nothing in a multi-line field", 0, submissions)
        assertEquals(listOf(KeyEvent.ACTION_DOWN, KeyEvent.ACTION_UP), injector.keys.map { it.event.action })
        assertEquals(1, reads)
    }

    @Test
    fun enterInASingleLineEditorStillRunsItsActionOnce() {
        var submissions = 0
        val editor = advertising { submissions++ }
        val input = Input(injector, clock, { EditorSettings("dev.app", 0x8001, 0x2000006) }) { editor }
        assertEquals("editor-action", input.key(KeyEvent.KEYCODE_ENTER))
        assertEquals(1, submissions)
        assertTrue("AND-006: no key after a successful action", injector.keys.isEmpty())
    }

    @Test
    fun anotherAppsOrUnknownKeyboardSettingsAreNotTrusted() {
        var submissions = 0
        val editor = advertising { submissions++ }
        Input(injector, clock, { EditorSettings("other.app", 0x28001, 0x42000000) }) { editor }.key(KeyEvent.KEYCODE_ENTER)
        Input(injector, clock, { null }) { editor }.key(KeyEvent.KEYCODE_ENTER)
        assertEquals(2, submissions)
        assertTrue(injector.keys.isEmpty())
    }

    @Test
    fun keyboardSettingsAreReadOnlyWhenEnterCouldBeAnEditorAction() {
        var reads = 0
        val settings = { _: String? -> reads++; EditorSettings("dev.app", 0x28001, 0x42000000) }
        val cannot = object : Field by FakeField(value = "one") {
            override fun canSubmitIme() = false
        }
        Input(injector, clock, settings) { cannot }.key(KeyEvent.KEYCODE_ENTER)
        Input(injector, clock, settings) { advertising {} }.key(KeyEvent.KEYCODE_ENTER, longPress = true)
        Input(injector, clock, settings) { null }.key(KeyEvent.KEYCODE_ENTER)
        Input(injector, clock, settings) { advertising {} }.key(KeyEvent.KEYCODE_TAB)
        assertEquals("dumpsys costs ~150 ms: not for keys that cannot become an action", 0, reads)
    }

    // Issue #143: the settings came from the full `dumpsys input_method` before every Enter that
    // could become the IME action, 220-300 ms on the bench emulator: the keyboard app and the
    // app's input client write their own state into it. Enter in a single-line field went from
    // about 5 ms to 0.3-0.5 s. The input method service's own state, where the current
    // EditorInfo is, comes alone from its proto dump (about 15 ms).
    @Test
    fun enterInASingleLineEditorDoesNotRunTheFullInputMethodDump() {
        val device = FakeDevice().apply {
            answer = { cmd ->
                if (cmd == "dumpsys input_method --proto") ShellResult(0, "", false, ImeTrace.singleLineCompose)
                else ShellResult(0, "curEditorInfo:\n  inputType=0x8001 imeOptions=0x2000006\n  packageName=dev.testbuddy.bench", false)
            }
        }
        var submissions = 0
        val editor = advertising("dev.testbuddy.bench") { submissions++ }
        val input = Input(injector, clock, { EditorSettings.read(device) }) { editor }
        repeat(3) { assertEquals("editor-action", input.key(KeyEvent.KEYCODE_ENTER)) }
        assertEquals(3, submissions)
        assertTrue(injector.keys.isEmpty())
        assertEquals("full input_method dumps for 3 Enters, each 220-300 ms on a device: ${device.shells}",
            0, device.shells.count { it == "dumpsys input_method" })
    }

    // Issue #99: the settings are read before every Enter that could become the IME action, in
    // a shell process each time. Not kept per editor (#99 review): a Jetpack Compose field can
    // restart input with other settings on the same accessibility node, which reports neither
    // its input type nor multi-line, and right after a focus change several reads in a row can
    // still show the editor before; a kept answer would then decide every later Enter in the
    // field wrongly. Each read is made cheap instead: the proto dump (about 15 ms), now on
    // Android 12-14 too.
    private fun imeDevice(sdk: Int = 36, trace: () -> ByteArray) = FakeDevice().apply {
        this.sdk = sdk
        answer = { cmd ->
            if (cmd == "dumpsys input_method --proto") trace().let { ShellResult(0, String(it, Charsets.ISO_8859_1), false, it) }
            else ShellResult(0, "", false)
        }
    }
    private val composeMultiLine = ImeTrace.of(0x28001, 0x42000000, "dev.app")
    private val singleLineDone = ImeTrace.of(0x8001, 0x2000006, "dev.app")

    @Test
    fun aComposeFieldRestartingInputWithOtherSettingsIsFollowed() {
        // The same node throughout: Compose reports no input type, so nothing on it changes.
        var trace = singleLineDone
        val device = imeDevice { trace }
        var submissions = 0
        val editor = advertising { submissions++ }
        val input = Input(injector, clock, { EditorSettings.read(device) }) { editor }
        repeat(2) { assertEquals("editor-action", input.key(KeyEvent.KEYCODE_ENTER)) }
        trace = composeMultiLine
        assertEquals("the field restarted input as multi-line", "hardware", input.key(KeyEvent.KEYCODE_ENTER))
        assertEquals(2, submissions)
    }

    @Test
    fun readsStillShowingThePreviousEditorDecideNoLaterEnter() {
        // Focus has just moved to a multi-line editor; the first two reads still show the
        // single-line editor before. Those Enters act as the reads say; the later ones follow
        // the editor's own settings.
        val reads = ArrayDeque(listOf(singleLineDone, singleLineDone))
        val device = imeDevice { reads.removeFirstOrNull() ?: composeMultiLine }
        var submissions = 0
        val editor = advertising { submissions++ }
        val input = Input(injector, clock, { EditorSettings.read(device) }) { editor }
        repeat(2) { input.key(KeyEvent.KEYCODE_ENTER) }
        repeat(3) { assertEquals("Enter ${it + 3}", "hardware", input.key(KeyEvent.KEYCODE_ENTER)) }
        assertEquals(2, submissions)
    }

    @Test
    fun onAndroid12To14EachEnterIsOneProtoRead() {
        // Issue #99 review: there `--proto` prints the service's state bare (no IMMTRACE, no
        // entry), which was not taken for a trace: every Enter also ran the full text dump
        // (220-300 ms, and it can wait up to 5 s on the app's main thread).
        for (sdk in 31..34) {
            val device = imeDevice(sdk) { ImeTrace.bare(0x28001, 0x42000000, "dev.app") }
            var submissions = 0
            val editor = advertising { submissions++ }
            val input = Input(injector, clock, { EditorSettings.read(device) }) { editor }
            repeat(3) { assertEquals("API $sdk", "hardware", input.key(KeyEvent.KEYCODE_ENTER)) }
            assertEquals("API $sdk", 0, submissions)
            assertEquals("API $sdk: ${device.shells}", List(3) { "dumpsys input_method --proto" }, device.shells)
        }
    }

    // Issue #70: on Android 11 (no proto dump: the text one) the current editor is the app's
    // client copy; read with the names of API 34+ only, a multi-line Compose field's Enter ran
    // the IME action again.
    @Test
    fun enterInAMultiLineComposeEditorTypesANewLineOnAndroid11() {
        val dump = "Client:\n  InputMethodManager:\n    mCurrentTextBoxAttribute:\n" +
            "      inputType=0x28001 imeOptions=0x42000000 privateImeOptions=null\n" +
            "      actionLabel=null actionId=0\n      packageName=dev.app autofillId=1073741824 fieldId=-1 fieldName=null\n"
        val device = FakeDevice().apply { sdk = 30; shellResult = ShellResult(0, dump, false) }
        var submissions = 0
        val input = Input(injector, clock, { EditorSettings.read(device) }) { advertising { submissions++ } }
        assertEquals("hardware", input.key(KeyEvent.KEYCODE_ENTER))
        assertEquals("the IME action types nothing in a multi-line field", 0, submissions)
    }

    @Test
    fun theClientSectionOfTheFieldsAppIsRead() {
        // Android 11-13 also dump the client of the last focused window "just in case": two
        // client sections, the first another app's. The field's app picks its own.
        fun client(pkg: String, inputType: String, imeOptions: String) =
            "Client:\n  InputMethodManager:\n    mCurrentTextBoxAttribute:\n" +
                "      inputType=$inputType imeOptions=$imeOptions privateImeOptions=null\n" +
                "      actionLabel=null actionId=0\n      packageName=$pkg autofillId=1073741824 fieldId=-1 fieldName=null\n"
        val dump = client("other.app", "0x8001", "0x2000006") + client("dev.app", "0x28001", "0x42000000")
        val device = FakeDevice().apply { sdk = 30; shellResult = ShellResult(0, dump, false) }
        var submissions = 0
        val input = Input(injector, clock, { EditorSettings.read(device, it) }) { advertising { submissions++ } }
        assertEquals("hardware", input.key(KeyEvent.KEYCODE_ENTER))
        assertEquals(0, submissions)
    }

    @Test
    fun aLongKeyPressRepeatsWithTheLongPressFlag() {
        input.key(KeyEvent.KEYCODE_POWER, longPress = true)
        val (down, long, up) = injector.keys.map { it.event }
        assertEquals(1, long.repeat)
        assertEquals(KeyEvent.FLAG_LONG_PRESS, long.flags)
        assertEquals(600L, long.at - down.at)
        assertEquals(KeyEvent.ACTION_UP, up.action)
        assertEquals(down.downAt, up.downAt)
    }

    @Test
    fun typingSendsKeysInChunksEachChunksLastWaitedFor() {
        val text = "abcdefghijklmnopqrstuvwxyz" // 26 keys: a chunk of 16, then 10
        assertTrue(input.type(text))
        assertEquals(52, injector.keys.size)
        val waited = injector.keys.withIndex().filter { it.value.sync }.map { it.index }
        assertEquals(listOf(Input.KEY_CHUNK * 2 - 1, 51), waited)
        assertEquals(KeyEvent.KEYCODE_A, injector.keys.first().event.code)
    }

    @Test
    fun textWithNoKeysIsSetOnTheFocusedField() {
        field = FakeField("Hello world", selectionStart = 5, selectionEnd = 5)
        assertTrue(input.type(" 😀"))
        assertEquals("Hello 😀 world", field!!.value)
        assertEquals(5 + 3 to 5 + 3, field!!.selects.last())
        assertTrue(injector.keys.isEmpty())
    }

    @Test
    fun textWithNoKeysReplacesTheSelectionAndIgnoresAHint() {
        field = FakeField("Search", showingHint = true, selectionStart = 0, selectionEnd = 0)
        assertTrue(input.type("😀"))
        assertEquals("😀", field!!.value)
        field = FakeField("abcdef", selectionStart = 4, selectionEnd = 1)
        assertTrue(input.type("é"))
        assertEquals("aéef", field!!.value)
    }

    @Test
    fun textWithNoKeysAndNoFieldFails() {
        assertFalse(input.type("😀"))
        field = FakeField("x", editable = false)
        assertFalse(input.type("😀"))
    }

    @Test
    fun eraseMovesTheCursorToTheEndFirst() {
        field = FakeField("hello", selectionStart = 2, selectionEnd = 2)
        erase(3)
        assertEquals(5 to 5, field!!.selects.single())
        assertEquals(List(3) { listOf(KeyEvent.ACTION_DOWN, KeyEvent.ACTION_UP) }.flatten(), injector.keys.map { it.event.action })
        assertTrue(injector.keys.all { it.event.code == KeyEvent.KEYCODE_DEL })
        assertTrue(injector.keys.last().sync)
    }

    @Test
    fun eraseFallsBackToTheEndKey() {
        field = FakeField("hello", selectionStart = 2, selectionEnd = 2, acceptSelect = false)
        erase(1)
        assertEquals(KeyEvent.KEYCODE_MOVE_END, injector.keys.first().event.code)
        assertEquals(KeyEvent.KEYCODE_DEL, injector.keys.last().event.code)
    }

    /** Issue #289: a caret reported at the end is put there all the same: the report can be wrong. */
    @Test
    fun eraseSetsTheCaretEvenWhenItIsReportedAtTheEnd() {
        field = FakeField("hello")
        erase(2)
        assertEquals(listOf(5 to 5), field!!.selects)
        assertEquals(4, injector.keys.size)
    }

    private fun erase(n: Int) = input.erase(n, events)

    /** Issue #289: a field holding this, its caret left mid-text by a tap, reported at the end. */
    private fun tappedMidText(vararg off: String) = EraseField("QUICK BROWN FOX FAST", caret = 14, reportsCaretAtEnd = true).also {
        if ("select" in off) it.takesSelect = false
        if ("end" in off) it.takesEndKey = false
        custom = it
        injector.onKey = it::key
    }

    /** Issue #289: erasing all after a tap mid-text leaves the field empty, not its tail. */
    @Test
    fun eraseAllAfterATapMidTextLeavesTheFieldEmpty() {
        val f = tappedMidText()
        erase(40)
        assertEquals("", f.value)
    }

    /** Issue #289: erasing N takes N characters from the end, wherever the tap left the caret. */
    @Test
    fun eraseOfSomeAfterATapMidTextTakesThemFromTheEnd() {
        val f = tappedMidText()
        erase(5)
        assertEquals("QUICK BROWN FOX", f.value)
        assertTrue("erasing at the end needs no text set", f.sets.isEmpty())
    }

    /**
     * Issue #289: a field whose caret cannot be moved (no selection action, no End key) loses
     * what is before its caret to the DELs: erasing it all then empties it with a text set.
     */
    @Test
    fun eraseAllEmptiesAFieldWhoseCaretCannotBeMoved() {
        val f = tappedMidText("select", "end")
        erase(40)
        assertEquals("", f.value)
        assertEquals(listOf(""), f.sets)
    }

    /** Issue #289: erasing some there leaves the field with its text less the erased end. */
    @Test
    fun eraseOfSomeRepairsAFieldWhoseCaretCannotBeMoved() {
        val f = tappedMidText("select", "end")
        erase(5)
        assertEquals("QUICK BROWN FOX", f.value)
    }

    /** Issue #289: a field that takes neither the DELs nor a text set fails the step, naming what it holds. */
    @Test
    fun eraseFailsWhenTheFieldRejectsDeletes() {
        val f = EraseField("abc", takesDel = false, takesSetText = false)
        custom = f
        injector.onKey = f::key
        val e = assertThrows(IllegalStateException::class.java) { erase(10) }
        assertTrue(e.message, e.message!!.contains("the field holds \"abc\", expected \"\""))
    }

    /** Issue #289: a field that shows the DELs a frame later is waited for, a frame and no more. */
    @Test
    fun eraseWaitsForAFieldThatShowsTheDeletesAFrameLater() {
        val f = EraseField("hello")
        f.later = { del -> clock.postDelayed(Runnable { del() }, 16) }
        custom = f
        injector.onKey = f::key
        erase(50)
        assertEquals("", f.value)
        assertTrue("waited ${clock.now - 10_000} ms", clock.now - 10_000 <= 2 * Input.FRAME_MS)
    }

    /**
     * Issue #289, with #297's lesson: a field that shows the DELs one by one, 60 ms apart, is
     * behind past the cap but never still: it is waited for, and nothing more is deleted or set
     * on a read that is only lagging.
     */
    @Test
    fun eraseNeverRepairsOnALaggingRead() {
        val f = EraseField("hello world")
        var k = 0
        f.later = { del -> clock.postDelayed(Runnable { del() }, 60L * ++k) }
        custom = f
        injector.onKey = f::key
        erase(6)
        assertEquals("hello", f.value)
        assertTrue(f.sets.isEmpty())
        assertEquals(6, injector.keys.count { it.event.action == KeyEvent.ACTION_DOWN && it.event.code == KeyEvent.KEYCODE_DEL })
        assertNull(input.lastErase)
    }

    private fun erased(f: EraseField, n: Int): EraseField {
        custom = f
        injector.onKey = f::key
        erase(n)
        return f
    }

    /**
     * Issue #289 review: a field whose app writes text of its own gets no verdict, as for typing.
     * A phone field puts its "+1 " prefix back: no text set, and the step passes.
     */
    @Test
    fun eraseOfAFieldThatPutsItsPrefixBackGivesNoVerdict() {
        val f = erased(EraseField("+1 555", app = { if (it.startsWith("+1 ")) it else "+1 " }), 50)
        assertEquals("+1 ", f.value)
        assertNull("not put right: the reply must not say repaired", input.lastErase)
    }

    /**
     * Issue #289 re-review: a date mask that puts its separator back ("12/31" shown as "12/31/")
     * looks like text left over; the repair DEL it will not show is the app's own: no verdict.
     */
    @Test
    fun eraseOfAMaskThatPutsItsSeparatorBackGivesNoVerdict() {
        val f = erased(EraseField("12/31/2025", app = { if (Regex("\\d\\d/\\d\\d").matches(it)) "$it/" else it }), 5)
        assertEquals("12/31/", f.value)
        assertNull(input.lastErase)
    }

    /** Issue #289 review: a field that shows "0" when empty is the app's own: no verdict. */
    @Test
    fun eraseOfAFieldThatRefillsAZeroGivesNoVerdict() {
        val f = erased(EraseField("5", app = { it.ifEmpty { "0" } }), 50)
        assertEquals("0", f.value)
        assertTrue(f.sets.isEmpty())
    }

    /** Issue #289 review: a date mask that drops a trailing "/" is the app's own: no text set, no verdict. */
    @Test
    fun eraseOfADateMaskThatDropsItsSlashGivesNoVerdict() {
        val f = erased(EraseField("12/05/2024", app = { it.trimEnd('/') }), 4)
        assertEquals("12/05", f.value)
        assertTrue(f.sets.isEmpty())
    }

    /** Issue #289 review: a field still changing after the reads gets no verdict, as typing's after its rounds. */
    @Test
    fun eraseOfAFieldThatKeepsChangingGivesNoVerdict() {
        val f = EraseField("hello", takesDel = false)
        clock.repeat(clock.now + 50, 50, clock.now + 10_000) { f.value = if (f.value == "hello") "hellO" else "hello" }
        erased(f, 50)
        assertTrue(f.sets.isEmpty())
    }

    /** Issue #289 review: a repair that the field refused is not reported as made. */
    @Test
    fun eraseSaysWhenTheTextCouldNotBeSet() {
        val f = tappedMidText("select", "end")
        f.takesSetText = false
        val e = assertThrows(IllegalStateException::class.java) { erase(40) }
        assertTrue(e.message, e.message!!.contains("the field holds \"X FAST\", expected \"\""))
        assertTrue(e.message, e.message!!.contains("refused"))
    }

    /**
     * Issue #289, as seen on the device: a Compose field reported its caret where the tap left
     * it (12 of 18), took ACTION_SET_SELECTION (true), and moved the caret a frame later. DELs
     * sent at once deleted before the old caret. The DELs wait for the caret to show at the end:
     * the field is erased by the DELs themselves, with no repair.
     */
    @Test
    fun eraseWaitsForTheCaretToReachTheEndBeforeDeleting() {
        for (n in listOf(40, 5)) {
            val f = EraseField("SPEEDY RUNS FAST", caret = 12)
            f.selectLater = { move -> clock.postDelayed(Runnable { move() }, 16) }
            erased(f, n)
            assertEquals(if (n == 40) "" else "SPEEDY RUNS", f.value)
            assertTrue("the field needed no text set: ${f.sets}", f.sets.isEmpty())
            assertNull(input.lastErase)
        }
    }

    /** Issue #289: a field that echoes the DELs costs one read: no wait. */
    @Test
    fun eraseOfAFieldThatEchoesWaitsForNothing() {
        val f = EraseField("hello world")
        custom = f
        injector.onKey = f::key
        erase(6)
        assertEquals("hello", f.value)
        assertEquals(10_000L, clock.now)
    }

    /** Issue #289: a DEL takes an emoji with its skin tone whole, counted as one character. */
    @Test
    fun eraseCountsAnEmojiWithItsSkinToneAsOneCharacter() {
        val f = EraseField("ab👍🏽")
        custom = f
        injector.onKey = f::key
        erase(1)
        assertEquals("ab", f.value)
    }

    /**
     * Issue #289: code boxes whose DEL in an empty box moves the focus back: the DELs go on to
     * the box focused, as a keyboard's would, and the focus that moved gets no verdict.
     */
    @Test
    fun eraseAcrossCodeBoxesFollowsTheFocusWithNoVerdict() {
        val first = EraseField("1")
        val second = EraseField("2")
        custom = second
        injector.onKey = { k ->
            val f = custom as EraseField
            if (k.action == KeyEvent.ACTION_DOWN && k.code == KeyEvent.KEYCODE_DEL && f === second && f.value.isEmpty()) custom = first
            else f.key(k)
        }
        erase(50)
        assertEquals("", first.value)
        assertEquals("", second.value)
    }

    /** Issue #289: a field that is no plain text field (custom, masked) gets no verdict, as for typing. */
    @Test
    fun eraseOfACustomFieldGivesNoVerdict() {
        field = FakeField("abc") // not native plain text, ignores keys
        erase(10)
        assertEquals("abc", field!!.value)
    }

    @Test
    fun clearEmptiesTheFocusedFieldInOneAction() {
        assertFalse(input.clearFocused())
        field = FakeField("secret")
        assertTrue(input.clearFocused())
        assertEquals("", field!!.value)
        assertTrue(injector.keys.isEmpty())
    }
}
