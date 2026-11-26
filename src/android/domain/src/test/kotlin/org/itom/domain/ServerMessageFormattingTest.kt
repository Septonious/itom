package org.itom.domain

import org.junit.Test
import kotlin.test.*

class ServerMessageFormattingTest {
    @Test fun requestedMixedColors() {
        val runs = ServerMessageFormatting.parse("&cHello &aWorld")
        assertEquals(listOf("Hello ", "World"), runs.map { it.text })
        assertEquals(listOf(0xFF5555, 0x55FF55), runs.map { it.style.colorRgb })
    }

    @Test fun allSixteenColorsAreCaseInsensitive() {
        val expected = listOf(0x000000, 0x0000AA, 0x00AA00, 0x00AAAA,
            0xAA0000, 0xAA00AA, 0xFFAA00, 0xAAAAAA,
            0x555555, 0x5555FF, 0x55FF55, 0x55FFFF,
            0xFF5555, 0xFF55FF, 0xFFFF55, 0xFFFFFF)
        val runs = ServerMessageFormatting.parse("0123456789ABCDEF".map { "&${it}x" }.joinToString(""))
        assertEquals(expected, runs.map { it.style.colorRgb })
        assertEquals("x".repeat(16), runs.joinToString("") { it.text })
    }

    @Test fun stylesAccumulateAndReset() {
        val runs = ServerMessageFormatting.parse("&c&lBold &oItalic &nUnder &mStrike&rPlain")
        assertTrue(runs[0].style.bold)
        assertTrue(runs[1].style.bold && runs[1].style.italic)
        assertTrue(runs[2].style.underline)
        assertTrue(runs[3].style.strikethrough && runs[3].style.underline)
        assertEquals(ServerMessageStyle(), runs.last().style)
    }

    @Test fun colorCodeResetsPriorStyle() {
        val runs = ServerMessageFormatting.parse("&lBold&aGreen")
        assertTrue(runs.first().style.bold)
        assertEquals(ServerMessageStyle(colorRgb = 0x55FF55), runs.last().style)
    }

    @Test fun escapedUnknownAndTrailingAmpersandsStayReadable() {
        val raw = "R&&D &&c literal &zUnknown &"
        assertEquals("R&D &c literal &zUnknown &", ServerMessageFormatting.parse(raw).joinToString("") { it.text })
    }

    @Test fun unicodeAndNewlinesArePreservedWithoutEmptyRuns() {
        assertEquals("Привет 🌍\n世界", ServerMessageFormatting.parse("&bПривет 🌍\n&f世界").joinToString("") { it.text })
        assertTrue(ServerMessageFormatting.parse("&c&r&l").isEmpty())
        assertTrue(ServerMessageFormatting.parse("").isEmpty())
    }
}
