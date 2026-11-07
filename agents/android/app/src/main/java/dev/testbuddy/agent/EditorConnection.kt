package dev.testbuddy.agent

import android.view.accessibility.AccessibilityNodeInfo

const val EDITOR_SOCKET = "testbuddy-editor"

fun editorTarget(n: AccessibilityNodeInfo) = EditorTarget(
    n.packageName?.toString().orEmpty(), n.windowId, n.hashCode(),
    n.viewIdResourceName.orEmpty(), n.className?.toString().orEmpty(),
)
