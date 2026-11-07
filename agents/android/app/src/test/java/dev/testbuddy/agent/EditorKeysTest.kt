package dev.testbuddy.agent

import org.junit.Assert.*
import org.junit.Test

/** AND-005: session readiness callbacks must not poll, retry or cross a focus boundary. */
class EditorKeysTest {
    private val target = EditorTarget("example.app", 7, 42, "field", "EditText")
    private val clock = FakeClock()

    @Test
    fun aReadyCallbackDispatchesOnceWithoutPollingOrSleeping() {
        var ready = false
        var checks = 0
        var sent = 0
        val replies = mutableListOf<String>()
        val keys = EditorKeys({ clock.postDelayed(it, 1000) }, clock::cancel, {
            checks++
            if (ready) { sent++; EditorAttempt.SENT } else EditorAttempt.WAIT
        }, { true })
        keys.request(target, replies::add)
        clock.advance(80)
        assertEquals(1, checks)
        assertTrue(replies.isEmpty())
        ready = true; keys.changed()
        assertEquals(listOf("sent"), replies)
        assertEquals(1, sent)
        repeat(3) { keys.changed() }; clock.advance(2000)
        assertEquals(2, checks)
        assertEquals(1, sent)
        assertEquals(0, clock.pending)
    }

    @Test
    fun unavailableConnectionFallsBackOnlyWhileTheSameFieldHasFocus() {
        for (same in listOf(true, false)) {
            val replies = mutableListOf<String>()
            val keys = EditorKeys({ clock.postDelayed(it, 1000) }, clock::cancel,
                { EditorAttempt.WAIT }, { same })
            keys.request(target, replies::add)
            clock.advance(1000)
            assertEquals(listOf(if (same) "unavailable" else "rejected"), replies)
            assertEquals(0, clock.pending)
        }
    }

    @Test
    fun changedFocusRejectsRatherThanDeletingInAnotherField() {
        var focus = target
        var sends = 0
        val replies = mutableListOf<String>()
        val keys = EditorKeys({ clock.postDelayed(it, 1000) }, clock::cancel, {
            if (it != focus) EditorAttempt.REJECTED else { sends++; EditorAttempt.SENT }
        }, { it == focus })
        focus = target.copy(node = 43)
        keys.request(target, replies::add)
        assertEquals(listOf("rejected"), replies)
        assertEquals(0, sends)
    }

    @Test
    fun anExceptionAfterDispatchCannotTriggerHardwareFallback() {
        val replies = mutableListOf<String>()
        var sends = 0
        val keys = EditorKeys({ clock.postDelayed(it, 1000) }, clock::cancel, {
            sends++; throw IllegalStateException("reply lost")
        }, { true })
        keys.request(target, replies::add)
        keys.changed(); clock.advance(2000)
        assertEquals(listOf("error"), replies)
        assertEquals(1, sends)
    }

    @Test
    fun disablingTheServiceCancelsAWaitingKey() {
        val replies = mutableListOf<String>()
        var ready = false
        var sends = 0
        val keys = EditorKeys({ clock.postDelayed(it, 1000) }, clock::cancel, {
            if (ready) { sends++; EditorAttempt.SENT } else EditorAttempt.WAIT
        }, { true })
        keys.request(target, replies::add); keys.close()
        ready = true; keys.changed(); clock.advance(2000)
        assertEquals(listOf("rejected"), replies)
        assertEquals(0, sends)
    }
}
