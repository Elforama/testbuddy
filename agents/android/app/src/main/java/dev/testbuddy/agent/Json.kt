package dev.testbuddy.agent

/** A small, allocation-light JSON writer: the tree is written on every read. */
class Json(capacity: Int = 4096) {
    val sb = StringBuilder(capacity)
    private var needComma = false

    fun reset(): Json { sb.setLength(0); needComma = false; return this }

    private fun comma() { if (needComma) sb.append(','); needComma = false }

    fun obj(): Json { comma(); sb.append('{'); return this }
    fun endObj(): Json { sb.append('}'); needComma = true; return this }
    fun arr(): Json { comma(); sb.append('['); return this }
    fun endArr(): Json { sb.append(']'); needComma = true; return this }

    fun key(k: String): Json { comma(); sb.append('"').append(k).append("\":"); return this }

    fun str(v: CharSequence?): Json {
        comma()
        if (v == null) { sb.append("null"); needComma = true; return this }
        sb.append('"')
        for (i in 0 until v.length) {
            val c = v[i]
            when {
                c == '"' -> sb.append("\\\"")
                c == '\\' -> sb.append("\\\\")
                c == '\n' -> sb.append("\\n")
                c == '\r' -> sb.append("\\r")
                c == '\t' -> sb.append("\\t")
                c < ' ' -> sb.append(String.format("\\u%04x", c.code))
                c == ' ' || c == ' ' -> sb.append(String.format("\\u%04x", c.code))
                else -> sb.append(c)
            }
        }
        sb.append('"')
        needComma = true
        return this
    }

    fun num(v: Long): Json { comma(); sb.append(v); needComma = true; return this }
    fun num(v: Int): Json { comma(); sb.append(v); needComma = true; return this }
    fun num(v: Double): Json { comma(); sb.append(v); needComma = true; return this }
    fun bool(v: Boolean): Json { comma(); sb.append(if (v) "true" else "false"); needComma = true; return this }
    fun raw(v: String): Json { comma(); sb.append(v); needComma = true; return this }

    fun put(k: String, v: CharSequence?): Json = key(k).str(v)
    fun put(k: String, v: Long): Json = key(k).num(v)
    fun put(k: String, v: Int): Json = key(k).num(v)
    fun put(k: String, v: Double): Json = key(k).num(v)
    fun put(k: String, v: Boolean): Json = key(k).bool(v)

    override fun toString(): String = sb.toString()
}
