package dev.testbuddy.agent

import org.json.JSONObject

/** Node identity shared by UiAutomation and the service; no field text is transported. */
data class EditorTarget(val pkg: String, val window: Int, val node: Int, val id: String, val cls: String) {
    companion object {
        fun fromJson(value: JSONObject) = EditorTarget(
            value.getString("pkg"), value.getInt("window"), value.getInt("node"),
            value.getString("id"), value.getString("cls"),
        )
    }
}

enum class EditorAttempt { SENT, WAIT }

/** One outstanding key. Recheck on input-session callbacks, never on a polling timer. */
class EditorKeys(
    private val schedule: (Runnable) -> Unit,
    private val cancel: (Runnable) -> Unit,
    private val attempt: (EditorTarget) -> EditorAttempt,
    private val focusedTarget: () -> EditorTarget?,
) {
    private class Pending(val target: EditorTarget, val done: (String) -> Unit, val timeout: Runnable)
    private var pending: Pending? = null

    fun request(target: EditorTarget, done: (String) -> Unit) {
        check(pending == null) { "an editor key is already pending" }
        val timeout = Runnable {
            // No key has been dispatched. The shell must verify the original
            // field before using hardware input when this service cannot send.
            finish("unavailable")
        }
        pending = Pending(target, done, timeout)
        changed()
        if (pending != null) schedule(timeout)
    }

    fun changed() {
        val p = pending ?: return
        try {
            val focus = focusedTarget() ?: return
            if (focus != p.target) {
                // Do not send through a different session. The shell will
                // check its own fresh editor identity before any fallback.
                finish("unavailable")
                return
            }
            when (attempt(p.target)) {
                EditorAttempt.SENT -> finish("sent")
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
