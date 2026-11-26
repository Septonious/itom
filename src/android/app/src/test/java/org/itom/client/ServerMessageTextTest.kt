package org.itom.client

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import org.junit.Assert.*
import org.junit.Test

class ServerMessageTextTest {
    @Test fun colorCodesReachRenderedTextSpans() {
        val message = serverMessageText("&cHello &aWorld")
        assertEquals("Hello World", message.text)
        assertEquals(2, message.spanStyles.size)
        assertEquals(Color(0xFFFF5555.toInt()), message.spanStyles[0].item.color)
        assertEquals(Color(0xFF55FF55.toInt()), message.spanStyles[1].item.color)
        assertEquals(0, message.spanStyles[0].start)
        assertEquals(6, message.spanStyles[1].start)
    }

    @Test fun resetRestoresThemeColorAndWeight() {
        val message = serverMessageText("&c&lAlert&r Normal")
        assertEquals("Alert Normal", message.text)
        assertEquals(FontWeight.Bold, message.spanStyles[0].item.fontWeight)
        assertEquals(Color.Unspecified, message.spanStyles[1].item.color)
        assertNull(message.spanStyles[1].item.fontWeight)
    }
}
