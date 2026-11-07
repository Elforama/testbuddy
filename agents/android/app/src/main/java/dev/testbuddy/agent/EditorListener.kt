package dev.testbuddy.agent

/** Release the listening endpoint even when another thread is blocked in accept. */
fun releaseEditorListener(shutdown: () -> Unit, close: () -> Unit, awaitClosed: () -> Unit) {
    try { shutdown() } catch (_: Throwable) {}
    try { close() } finally { awaitClosed() }
}
