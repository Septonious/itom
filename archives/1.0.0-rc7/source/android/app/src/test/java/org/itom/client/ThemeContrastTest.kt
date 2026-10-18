package org.itom.client

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import org.junit.Assert.assertTrue
import org.junit.Test

class ThemeContrastTest {
    private fun contrast(a: Color, b: Color): Float {
        val x = a.luminance(); val y = b.luminance()
        return (maxOf(x, y) + .05f) / (minOf(x, y) + .05f)
    }
    @Test fun textAndActionLabelsRemainReadableInDarkTheme() {
        assertTrue("Input boundaries need 3:1 contrast", contrast(Outline, Panel) >= 3f)
        listOf(Ink to Background, Ink to Panel, Muted to Panel, Muted to Background,
            OnAccent to Blue, OnAccent to Green, OnAccent to Color(0xFF4A98FF)).forEach { (text, surface) ->
            assertTrue("Text contrast ${contrast(text, surface)} is below 4.5", contrast(text, surface) >= 4.5f)
        }
    }
}
