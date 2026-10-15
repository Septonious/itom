package org.itom.domain

import org.junit.Test
import org.junit.Assert.assertEquals

class AccountScaleTest {
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
            assertEquals(count * 2, profile.totalLines)
            assertEquals(count, profile.accounts.map { it.credentialRef }.toSet().size)
        }
    }
}
