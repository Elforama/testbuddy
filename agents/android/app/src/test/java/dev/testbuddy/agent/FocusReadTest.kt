package dev.testbuddy.agent

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * The agent crashed as the emulator started (CI, PR #387): the system started an input session
 * before it let the accessibility service read windows, NoopService's onStartInput asked for the
 * focused editor, and findFocus threw `SecurityException: ... not allowed to perform
 * ACCESS_ACCESSIBILITY`. Nothing caught it, so the service's process died ("Test Buddy Agent keeps
 * stopping"), the next flow could not find its row and the keyboard flow lost its editor
 * connection. A read the system refuses is no focused editor: the next input session reads again.
 */
class FocusReadTest {
    private val target = EditorTarget("app", 1, 2, "app:id/name", "android.widget.EditText")

    @Test fun aRefusedFocusReadIsNoFocusedEditor() {
        assertNull(NoopService.readFocus {
            throw SecurityException("dev.testbuddy.agent from uid 10217 not allowed to perform ACCESS_ACCESSIBILITY")
        })
    }

    @Test fun aFocusReadReturnsTheEditor() {
        assertEquals(target, NoopService.readFocus { target })
        assertNull(NoopService.readFocus { null })
    }
}
