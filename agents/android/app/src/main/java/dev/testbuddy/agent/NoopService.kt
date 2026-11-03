package dev.testbuddy.agent

import android.accessibilityservice.AccessibilityService
import android.view.accessibility.AccessibilityEvent

/**
 * An accessibility service that does nothing. Jetpack Compose only sends accessibility
 * events while an accessibility service is enabled (a UiAutomation client is not counted),
 * so without one, Compose screens change silently and an event-driven runner cannot see
 * them. The host enables this service while the agent runs and removes it afterwards.
 */
class NoopService : AccessibilityService() {
    override fun onAccessibilityEvent(event: AccessibilityEvent?) {}
    override fun onInterrupt() {}
}
