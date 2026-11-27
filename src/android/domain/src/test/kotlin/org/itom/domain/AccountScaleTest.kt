package org.itom.domain

import org.junit.Test
import org.junit.Assert.assertEquals

class AccountScaleTest {
    @Test fun operationalProfilesNeedFourEnabledAccounts() {
        val accounts = (1..5).map { number ->
            MailAccount("a$number", "Mail $number", "imap.example.invalid",
                credentialRef = "keystore:scale-a$number", providerId = "scale")
        }
        val profile = ServerProfile(profileId = "scale", name = "Scale", serverId = "a".repeat(32),
            userId = "12345678", providerId = "scale", encryptionKeyRef = "keystore:scale-key",
            mailboxNamespace = "itom-scale", accounts = accounts)
        for (enabled in 1..3) {
            val invalid = profile.copy(accounts = accounts.mapIndexed { index, a -> a.copy(enabled = index < enabled) })
            val failure = kotlin.test.assertFailsWith<DomainError> { Validation.profile(invalid) }
            assertEquals("ACCOUNT_COUNT_MINIMUM_4", failure.code)
        }
        Validation.profile(profile.copy(accounts = accounts.take(4)))
    }

    @Test fun profilesBeyondOldLimitsPreserveEveryCredentialReference() {
        for (count in listOf(11, 65, 128)) {
            val accounts = (1..count).map { number ->
                MailAccount("a%02d".format(number), "Mail $number", "imap.example.invalid",
                    credentialRef = "keystore:scale-a$number", providerId = "scale")
            }
            val profile = ServerProfile(profileId = "scale", name = "Scale", serverId = "a".repeat(32),
                userId = "12345678", providerId = "scale", encryptionKeyRef = "keystore:scale-key",
                mailboxNamespace = "itom-scale", accounts = accounts)
            Validation.profile(profile)
            assertEquals(count * 4, profile.totalLines)
            assertEquals(count, profile.accounts.map { it.credentialRef }.toSet().size)
        }
    }
}
