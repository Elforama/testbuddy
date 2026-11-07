package dev.testbuddy.agent

import org.junit.Assert.*
import org.junit.Test

class EditorListenerTest {
    // AND-005 stress: closing a Unix listener from another thread leaves
    // accept's descriptor reference alive. A fast rebind then crashes the service.
    @Test
    fun stoppedEndpointCanBeReboundAfterAnIdleAccept() {
        var listenerReference = true
        var acceptReference = true
        var woken = false
        releaseEditorListener(
            { woken = true },
            { listenerReference = false },
            { if (woken) acceptReference = false },
        )
        assertFalse("address remains in use after service shutdown", listenerReference || acceptReference)
    }

    @Test
    fun anAlreadyShutdownEndpointStillClosesAndWaitsForItsWorker() {
        var closed = false
        var joined = false
        releaseEditorListener({ throw IllegalStateException("already shut down") }, { closed = true }, { joined = true })
        assertTrue(closed)
        assertTrue(joined)
    }
}
