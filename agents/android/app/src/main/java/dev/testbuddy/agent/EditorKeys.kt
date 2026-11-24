package dev.testbuddy.agent

import org.json.JSONObject

data class EditorTarget(val pkg: String, val window: Int, val node: Int, val id: String, val cls: String) {
    companion object {
        fun fromJson(value: JSONObject) = EditorTarget(
            value.getString("pkg"), value.getInt("window"), value.getInt("node"),
            value.getString("id"), value.getString("cls"),
        )
    }
}

enum class EditorAttempt { SENT, WAIT }

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
                finish("unavailable")
                return
            }
            when (attempt(p.target)) {
                EditorAttempt.SENT -> finish("sent")
                EditorAttempt.WAIT -> Unit
            }
        } catch (_: Throwable) {
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
