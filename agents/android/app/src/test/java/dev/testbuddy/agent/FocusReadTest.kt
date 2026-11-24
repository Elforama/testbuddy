package dev.testbuddy.agent

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

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
