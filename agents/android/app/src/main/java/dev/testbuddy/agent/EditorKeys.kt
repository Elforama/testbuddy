package dev.testbuddy.agent

/** Node identity shared by UiAutomation and the service; no field text is transported. */
data class EditorTarget(val pkg: String, val window: Int, val node: Int, val id: String, val cls: String)

enum class EditorAttempt { SENT, WAIT, REJECTED }

/** One outstanding key. Recheck on input-session callbacks, never on a polling timer. */
class EditorKeys(
    private val schedule: (Runnable) -> Unit,
    private val cancel: (Runnable) -> Unit,
    private val attempt: (EditorTarget) -> EditorAttempt,
    private val sameFocus: (EditorTarget) -> Boolean,
) {
    private class Pending(val target: EditorTarget, val done: (String) -> Unit, val timeout: Runnable)
    private var pending: Pending? = null

    fun request(target: EditorTarget, done: (String) -> Unit) {
        check(pending == null) { "an editor key is already pending" }
        val timeout = Runnable {
            val p = pending ?: return@Runnable
            // No key has been dispatched. Only a still-matching focus can safely
            // use the legacy route when this provider has no input connection.
            finish(if (sameFocus(p.target)) "unavailable" else "rejected")
        }
        pending = Pending(target, done, timeout)
        changed()
        if (pending != null) schedule(timeout)
    }

    fun changed() {
        val p = pending ?: return
        try {
            when (attempt(p.target)) {
                EditorAttempt.SENT -> finish("sent")
                EditorAttempt.REJECTED -> finish("rejected")
                EditorAttempt.WAIT -> Unit
            }
        } catch (_: Throwable) {
            // Delivery may already have happened. Never return "unavailable":
            // that would inject another DEL after a lost/uncertain reply.
            finish("error")
        }
    }

    fun close() = finish("rejected")

    private fun finish(result: String) {
        val p = pending ?: return
        pending = null
        cancel(p.timeout)
        p.done(result)
    }
}
