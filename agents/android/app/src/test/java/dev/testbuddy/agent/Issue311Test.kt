package dev.testbuddy.agent

import android.view.accessibility.AccessibilityEvent
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

class Issue311Test {
    private val config = File("src/main/res/xml/noop_service.xml").readText()

    private fun eventTypes(): List<String> =
        Regex("""android:accessibilityEventTypes="([^"]*)"""").find(config)!!.groupValues[1].split('|')

    @Test fun theServiceAsksForTheEventsAWebViewSends() {
        val types = eventTypes()
        val all = "typeAllMask" in types
        for (t in listOf("typeWindowContentChanged", "typeViewScrolled", "typeViewTextChanged", "typeWindowStateChanged")) {
            assertTrue("noop_service.xml asks for $t (it asks for $types)", all || t in types)
        }
    }

    @Test fun onlyWindowStateEventsWakeAPendingBackspace() {
        assertTrue(NoopService.wakesKeys(AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED))
        for (t in listOf(AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED, AccessibilityEvent.TYPE_VIEW_SCROLLED,
            AccessibilityEvent.TYPE_VIEW_TEXT_CHANGED, AccessibilityEvent.TYPE_VIEW_CLICKED)) {
            assertFalse("event type $t", NoopService.wakesKeys(t))
        }
    }
}
