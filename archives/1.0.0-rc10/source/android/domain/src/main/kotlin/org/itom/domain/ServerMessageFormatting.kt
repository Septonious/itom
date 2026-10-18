package org.itom.domain

data class ServerMessageStyle(
    val colorRgb: Int? = null,
    val bold: Boolean = false,
    val italic: Boolean = false,
    val underline: Boolean = false,
    val strikethrough: Boolean = false,
)

data class ServerMessageRun(val text: String, val style: ServerMessageStyle)

/** Minecraft-style legacy colors, readable static styles and literal &&. */
object ServerMessageFormatting {
    private val colors = intArrayOf(
        0x000000, 0x0000AA, 0x00AA00, 0x00AAAA,
        0xAA0000, 0xAA00AA, 0xFFAA00, 0xAAAAAA,
        0x555555, 0x5555FF, 0x55FF55, 0x55FFFF,
        0xFF5555, 0xFF55FF, 0xFFFF55, 0xFFFFFF,
    )

    fun parse(raw: String): List<ServerMessageRun> {
        val runs = mutableListOf<ServerMessageRun>()
        val text = StringBuilder()
        var style = ServerMessageStyle()
        fun flush() {
            if (text.isNotEmpty()) {
                runs += ServerMessageRun(text.toString(), style)
                text.setLength(0)
            }
        }
        var i = 0
        while (i < raw.length) {
            if (raw[i] == '&' && i + 1 < raw.length) {
                val code = raw[i + 1].lowercaseChar()
                if (code == '&') {
                    text.append('&')
                    i += 2
                    continue
                }
                val color = "0123456789abcdef".indexOf(code)
                val next = when {
                    color >= 0 -> ServerMessageStyle(colorRgb = colors[color])
                    code == 'r' -> ServerMessageStyle()
                    code == 'l' -> style.copy(bold = true)
                    code == 'o' -> style.copy(italic = true)
                    code == 'n' -> style.copy(underline = true)
                    code == 'm' -> style.copy(strikethrough = true)
                    else -> null
                }
                if (next != null) {
                    flush()
                    style = next
                    i += 2
                    continue
                }
            }
            text.append(raw[i++])
        }
        flush()
        return runs
    }
}
