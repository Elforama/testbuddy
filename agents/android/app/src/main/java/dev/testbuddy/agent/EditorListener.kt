package dev.testbuddy.agent

fun releaseEditorListener(shutdown: () -> Unit, close: () -> Unit, awaitClosed: () -> Unit) {
    try { shutdown() } catch (_: Throwable) {}
    try { close() } finally { awaitClosed() }
}
