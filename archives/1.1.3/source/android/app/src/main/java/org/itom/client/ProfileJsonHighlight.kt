package org.itom.client

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.input.OffsetMapping
import androidx.compose.ui.text.input.TransformedText
import androidx.compose.ui.text.input.VisualTransformation

/** Colour only: every character, newline, selection and cursor offset stays intact. */
class ProfileJsonHighlight : VisualTransformation {
    private val tokens = Regex("\"(?:[^\"\\\\]|\\\\.)*\"|\\b(?:true|false|null)\\b|-?[0-9]+(?:\\.[0-9]+)?(?:[eE][+-]?[0-9]+)?|[{}\\[\\]:,]")
    override fun filter(text: AnnotatedString): TransformedText {
        val result = AnnotatedString.Builder(text)
        tokens.findAll(text.text).forEach { token ->
            val color = when {
                token.value.startsWith('"') -> Color(0xFF67B6FF)
                token.value.first().isDigit() || token.value.startsWith('-') -> Color(0xFFFFC66D)
                token.value in setOf("true", "false", "null") -> Color(0xFFCB9FFF)
                else -> Color(0xFF89CDA4)
            }
            result.addStyle(SpanStyle(color = color), token.range.first, token.range.last + 1)
        }
        return TransformedText(result.toAnnotatedString(), OffsetMapping.Identity)
    }
}
