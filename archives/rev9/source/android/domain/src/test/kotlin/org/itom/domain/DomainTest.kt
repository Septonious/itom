package org.itom.domain

import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.*
import org.junit.Test
import java.util.Base64
import kotlin.test.*

class DomainTest {
    private fun profile() = ServerProfile(profileId = "local", name = "Server", serverId = "1234567890abcdef1234567890abcdef", userId = "12345678", providerId = "test",
        encryptionKeyRef = "keystore:pair", mailboxNamespace = "ITOM/12345678/unique",
        accounts = listOf(MailAccount("mail", "Mail", "imap.example.invalid", credentialRef = "keystore:mail", providerId = "test")))
    private fun invitation() = Invitation(2, profile(), "abcdef1234567890abcdef1234567890", Base64.getEncoder().encodeToString(ByteArray(32) { it.toByte() }),
        mapOf("keystore:mail" to MailCredential("test@example.invalid", "TEST_ONLY_PASSWORD")), expires = 2_100_000_000L)
        .also { Validation.invitation(it, 1L) } // Negative cases must start from a valid fixture.
    private fun error(code: String, block: () -> Unit) { assertEquals(code, assertFailsWith<DomainError>(block = block).code) }
    private fun policy(vpn: List<String> = emptyList(), block: List<String> = emptyList(), direct: List<String> = emptyList()) = RoutingPolicy(rules = AddressRules(vpn, block, direct))
    private fun editorVault(): VaultDocument {
        val i = invitation()
        return VaultDocument(profiles = listOf(i.profile), selectedProfileId = i.profile.profileId,
            revision = 7, credentials = i.credentials, keys = mapOf(i.profile.encryptionKeyRef to PairSecret(i.pairId, i.pskBase64)))
            .also(Validation::vault)
    }
    private fun secondProfile() = profile().copy(profileId = "second", serverId = "22222222222222222222222222222222",
        encryptionKeyRef = "keystore:second_pair", accounts = listOf(profile().accounts.single().copy(credentialRef = "keystore:second_mail")))
    private fun twoProfileVault(): VaultDocument {
        val v = editorVault(); val other = secondProfile()
        return v.copy(profiles = v.profiles + other,
            credentials = v.credentials + ("keystore:second_mail" to MailCredential("other@example.invalid", "OTHER_PASSWORD")),
            keys = v.keys + (other.encryptionKeyRef to PairSecret("22222222222222222222222222222222", invitation().pskBase64))).also(Validation::vault)
    }

    @Test fun rev9PinningRoundTripsAndRejectsHostnames() {
        val p = profile()
        val pinned = p.copy(accounts = p.accounts.map { it.copy(dialIP = "81.19.77.171") })
        Validation.profile(pinned)
        assertEquals(pinned, Codec.json.decodeFromString<ServerProfile>(Codec.exportPublic(pinned)))
        for (value in listOf("imap.example.org", "127.0.0.1:993", "[::1]", "fe80::1%wlan0", "0.0.0.0", "::", "239.0.0.1", "ff02::1")) {
            error("IMAP_DIAL_IP_INVALID") { Validation.profile(p.copy(accounts = p.accounts.map { it.copy(dialIP = value) })) }
        }
        val v = twoProfileVault()
        val startTLS = v.copy(profiles = v.profiles.map { profile -> profile.copy(accounts = profile.accounts.map { it.copy(tls = "starttls", dialIP = "81.19.77.171") }) })
        val session = ProfileEditing.open(startTLS, "local")
        val next = ProfileEditing.apply(startTLS, "local", session.revision, session.initial.copy(accounts = session.initial.accounts.map { it.copy(dialIP = "") }))
        assertEquals("", next.profiles.first().accounts.first().dialIP)
        assertEquals("starttls", next.profiles.first().accounts.first().tls)
    }

    @Test fun domainMatchesSelfAndDescendantsOnly() {
        val router = Router(policy(block = listOf("google.com")))
        listOf("google.com", "maps.google.com", "a.maps.google.com", "MAPS.GOOGLE.COM.").forEach { assertEquals(RouteAction.BLOCK, router.decide(host = it).action) }
        listOf("evilgoogle.com", "google.com.evil", "google.net").forEach { assertEquals(RouteAction.VPN, router.decide(host = it).action) }
    }
    @Test fun groupPriorityBeatsSpecificity() {
        val router = Router(policy(vpn = listOf("example.com"), block = listOf("ads.example.com"), direct = listOf("cdn.ads.example.com")))
        assertEquals(RouteAction.VPN, router.decide(host = "cdn.ads.example.com").action)
        assertEquals(RouteAction.BLOCK, Router(policy(block = listOf(".ru"), direct = listOf("mail.ru"))).decide(host = "mail.ru").action)
    }
    @Test fun tldAndIdn() {
        val router = Router(policy(direct = listOf(".ru", "пример.рф")))
        assertEquals(RouteAction.DIRECT, router.decide(host = "a.example.ru").action)
        assertEquals(RouteAction.VPN, router.decide(host = "example.ru.evil").action)
        assertEquals(RouteAction.DIRECT, router.decide(host = "xn--e1afmkfd.xn--p1ai").action)
    }
    @Test fun ipv4NetworkEdges() {
        val router = Router(policy(block = listOf("192.0.2.127/25")))
        assertEquals(RouteAction.BLOCK, router.decide(address = "192.0.2.0").action)
        assertEquals(RouteAction.BLOCK, router.decide(address = "192.0.2.127").action)
        assertEquals(RouteAction.VPN, router.decide(address = "192.0.2.128").action)
        assertEquals(RouteAction.DIRECT, Router(policy(direct = listOf("0.0.0.0/0"))).decide(address = "203.0.113.42").action)
    }
    @Test fun ipv6SubnetAndEmbeddedIpv4() {
        val router = Router(policy(block = listOf("2001:db8::/32", "::ffff:192.0.2.0/120")))
        assertEquals(RouteAction.BLOCK, router.decide(address = "2001:db8:ffff::1").action)
        assertEquals(RouteAction.VPN, router.decide(address = "2001:db9::1").action)
        assertEquals(RouteAction.BLOCK, router.decide(address = "::ffff:c000:201").action)
        assertEquals(RouteAction.BLOCK, router.decide(address = "::ffff:192.0.2.1").action)
        assertEquals(RouteAction.VPN, router.decide(address = "192.0.2.1").action)
    }
    @Test fun unsafeOrAmbiguousRulesAreRejected() {
        listOf("010.0.0.1", "999.0.0.1", "1.2.3.4/33", "2001:db8::/129", "1::2::3", "[::1]", "fe80::1%eth0", "*.example.com", "https://example.com", ".co.uk", "example..com", "example.com:443", "a@b.com").forEach {
            assertFailsWith<DomainError>(message = it) { CompiledRule.parse(it) }
        }
    }
    @Test fun hostnameAndResolvedIpUseSameGroupPriority() {
        val router = Router(policy(vpn = listOf("192.0.2.0/24"), block = listOf("blocked.example")))
        assertEquals(RouteAction.VPN, router.decide("blocked.example", "192.0.2.1").action)
    }
    @Test fun excludedAppsBypassBlock() {
        val router = Router(policy(block = listOf("google.com")))
        val excluded = AppPolicy(AppMode.EXCLUDE_SELECTED, listOf("org.example.app"))
        val decision = router.decide(host = "maps.google.com", packageName = "org.example.app", apps = excluded)
        assertEquals(RouteAction.DIRECT, decision.action); assertTrue(decision.appBypass)
        assertEquals(RouteAction.BLOCK, router.decide(host = "maps.google.com", packageName = "org.other.app", apps = excluded).action)
    }
    @Test fun includedAppStillUsesAddressRules() {
        val router = Router(policy(direct = listOf(".ru")))
        val included = AppPolicy(AppMode.INCLUDE_SELECTED, listOf("org.example.app"))
        assertEquals(RouteAction.DIRECT, router.decide(host = "mail.ru", packageName = "org.example.app", apps = included).action)
        assertTrue(router.decide(host = "example.org", packageName = "org.other.app", apps = included).appBypass)
    }
    @Test fun emptyIncludeAndRemovedPackagesRejected() {
        error("EMPTY_INCLUDE") { AppSelection.validate(AppPolicy(AppMode.INCLUDE_SELECTED), emptyList()) }
        error("APP_NOT_INSTALLED") { AppSelection.validate(AppPolicy(AppMode.EXCLUDE_SELECTED, listOf("org.removed.app")), emptyList()) }
    }
    @Test fun sharedUidSelectionIsAtomic() {
        val catalog = listOf(InstalledApp("android", "System", true, 1000), InstalledApp("org.system.helper", "Helper", true, 1000), InstalledApp("org.other.app", "Other", false, 1001))
        val selected = AppSelection.toggle(emptySet(), catalog[0], catalog)
        assertEquals(setOf("android", "org.system.helper"), selected)
        AppSelection.validate(AppPolicy(AppMode.EXCLUDE_SELECTED, selected.toList()), catalog)
        error("SHARED_UID_PARTIAL_SELECTION") { AppSelection.validate(AppPolicy(AppMode.EXCLUDE_SELECTED, listOf("android")), catalog) }
        assertTrue(AppSelection.toggle(selected, catalog[1], catalog).isEmpty())
    }
    @Test fun providersCannotBeMixed() {
        val p = profile()
        error("MIXED_PROVIDERS") { Validation.profile(p.copy(accounts = p.accounts + p.accounts[0].copy(id = "other", providerId = "other", credentialRef = "keystore:other"))) }
    }
    @Test fun invalidUidAndInsecureTlsRejected() {
        listOf("01234567", "1234567", "123456789", "1234567x").forEach { uid -> error("USER_ID") { Validation.profile(profile().copy(userId = uid)) } }
        val p = profile(); error("TLS_REQUIRED") { Validation.profile(p.copy(accounts = listOf(p.accounts[0].copy(verifyCertificate = false)))) }
    }
    @Test fun renamingDoesNotChangeServerIdentity() {
        val old = profile(); val renamed = old.copy(name = "Renamed")
        Validation.profile(renamed)
        assertEquals(old.serverId, renamed.serverId); assertEquals(old.userId, renamed.userId); assertEquals(old.mailboxNamespace, renamed.mailboxNamespace)
    }
    @Test fun invitationLinkAndMultilineLint() {
        val value = invitation()
        val raw = Codec.json.encodeToString(value)
        val link = Codec.INVITATION_LINK_PREFIX + Base64.getUrlEncoder().withoutPadding().encodeToString(raw.toByteArray(Charsets.UTF_8))
        // Invitation and MailCredential deliberately use reference equality.
        // Compare every serialized field without putting credentials in failure output.
        val expected = Codec.json.parseToJsonElement(raw)
        for (text in listOf(link, Codec.formatInvitation(link))) {
            val parsed = Codec.invitation(text, 1L)
            assertTrue(value !== parsed, "Import must create a separate invitation")
            assertTrue(expected == Codec.json.encodeToJsonElement(parsed), "Invitation import changed serialized fields")
        }
        val malformed = "{\n  \"a\": 1,\n  \"b\": ]\n}"
        val issue = assertNotNull(Codec.lintInvitation(malformed, 1L))
        assertEquals("INVALID_JSON", issue.code)
        assertEquals(3, issue.line)
        assertEquals(8, issue.column)
        error("INVITATION_FORMAT") { Codec.invitation(Codec.INVITATION_LINK_PREFIX + "_w", 1L) }
        error("INVITATION_FORMAT") { Codec.invitation(link + "=", 1L) }
        error("DUPLICATE_JSON_KEY") { Codec.formatInvitation("{\"a\":1,\"a\":2}") }
    }
    @Test fun invitationRoundTripAndSecretRedaction() {
        val value = invitation(); val parsed = Codec.invitation(Codec.json.encodeToString(value), 1_000_000)
        assertEquals(value.profile, parsed.profile)
        assertFalse(value.toString().contains("TEST_ONLY_PASSWORD"))
        assertFalse(value.credentials.values.first().toString().contains("TEST_ONLY_PASSWORD"))
        assertEquals("PairSecret(<redacted>)", PairSecret("x", "secret").toString())
    }
    @Test fun expiredInvitationRejected() { error("INVITATION_EXPIRED") { Validation.invitation(invitation(), 2_100_000_000L) } }
    @Test fun invitationMustHaveExactSecretReferences() {
        val i = invitation()
        error("INVITATION_CREDENTIALS") { Validation.invitation(Invitation(2, i.profile, i.pairId, i.pskBase64, emptyMap(), i.expires), 1) }
    }
    @Test fun loginNewlinesRejected() {
        val i = invitation()
        error("CREDENTIAL_FORMAT") { Validation.invitation(Invitation(2, i.profile, i.pairId, i.pskBase64, mapOf("keystore:mail" to MailCredential("a\r\nLOGOUT", "pass")), i.expires), 1) }
    }
    @Test fun duplicateKeysIncludingEscapedKeysRejected() {
        error("DUPLICATE_JSON_KEY") { Codec.preflight("{\"a\":1,\"a\":2}", 1000) }
        error("DUPLICATE_JSON_KEY") { Codec.preflight("{\"nested\":{\"a\":1,\"\\u0061\":2}}", 1000) }
        Codec.preflight("{\"one\":{\"a\":1},\"two\":{\"a\":2}}", 1000)
    }
    @Test fun malformedAndDeepDocumentsRejected() {
        listOf("{} garbage", "[1,]", "{\"x\":}", "{\"x\":1,}", "\"unterminated").forEach { assertFailsWith<DomainError> { Codec.preflight(it, 1000) } }
        error("JSON_TOO_DEEP") { Codec.preflight("[".repeat(34) + "1" + "]".repeat(34), 1000) }
        error("DOCUMENT_TOO_LARGE") { Codec.preflight("\"ЖЖ\"", 5) }
    }
    @Test fun unknownInvitationFieldsRejected() {
        val data = Codec.json.encodeToJsonElement(invitation()).jsonObject
        error("INVITATION_FORMAT") { Codec.invitation(JsonObject(data + ("insecure" to JsonPrimitive(true))).toString(), 1) }
    }
    @Test fun migrationPreservesRulesAndUsesAllApps() {
        val s = AppSettings(routing = policy(block = listOf("example.com")))
        val old = Codec.json.encodeToJsonElement(s).jsonObject.toMutableMap().apply { put("schema_version", JsonPrimitive(2)); remove("app_routing") }
        val migrated = Codec.settings(JsonObject(old).toString())
        assertEquals(s.routing, migrated.routing); assertEquals(AppPolicy(), migrated.appRouting); assertEquals(5, migrated.schemaVersion); assertFalse(migrated.diagnosticsEnabled); assertEquals(RttThresholds(), migrated.rttThresholds)
    }
    @Test fun schema4MigrationDisablesDiagnosticsAndUsesNewRttBands() {
        val old = Codec.json.encodeToJsonElement(AppSettings()).jsonObject.toMutableMap().apply {
            put("schema_version", JsonPrimitive(4))
            put("rtt_color_thresholds_ms", buildJsonObject { put("green_max", 150); put("yellow_max", 400) })
            remove("diagnostics_enabled")
        }
        val migrated = Codec.settings(JsonObject(old).toString())
        assertEquals(5, migrated.schemaVersion)
        assertFalse(migrated.diagnosticsEnabled)
        assertEquals(RttThresholds(399, 799), migrated.rttThresholds)
    }

    @Test fun changedPriorityAndDirectFallbackRejected() {
        error("ROUTING_PRIORITY") { Validation.settings(AppSettings(routing = policy().copy(priority = listOf(RouteAction.BLOCK, RouteAction.VPN, RouteAction.DIRECT)))) }
        error("UNSUPPORTED_TRAFFIC_POLICY") { Validation.settings(AppSettings(traffic = TrafficPolicy(automaticDirectFallback = true))) }
    }
    @Test fun missingVaultSecretsAndDuplicateServerRejected() {
        val p = profile()
        error("VAULT_CREDENTIALS") { Validation.vault(VaultDocument(profiles = listOf(p), selectedProfileId = p.profileId)) }
        error("DUPLICATE_SERVER") { Validation.vault(VaultDocument(profiles = listOf(p, p.copy(profileId = "second")))) }
    }
    @Test fun publicExportOmitsSecretMaterial() {
        val json = Codec.exportPublic(profile())
        assertFalse(json.contains("psk_base64")); assertFalse(json.contains("password")); assertFalse(json.contains("username"))
    }
    @Test fun stopInvalidatesLateConnectionAndMetrics() {
        val machine = SessionMachine(); val generation = machine.begin("p", 1, 20)
        machine.stop()
        assertFalse(machine.connected(generation, 0)); assertFalse(machine.metrics(generation, Metrics(1000, 1000, 30, 1, 1, 20)))
        assertEquals(Phase.OFF, machine.state.value.phase); assertFalse(machine.state.value.vpnInterfaceUp)
    }
    @Test fun oldGenerationCannotFailNewSession() {
        val machine = SessionMachine(); val old = machine.begin("p", 1, 20)
        val current = machine.begin("p", 2, 20)
        assertFalse(machine.fail(old, "OLD_ERROR")); assertFalse(machine.connected(old, 0))
        assertTrue(machine.connected(current, 1)); assertEquals(2L, machine.state.value.configRevision)
    }
    @Test fun staleNotificationStopCannotStopNewSession() {
        val machine = SessionMachine()
        val old = machine.begin("old", 1, 2)
        machine.stop()
        val current = machine.begin("new", 2, 20)
        machine.connected(current, 100)
        val before = machine.state.value
        assertFalse(machine.stop(old))
        assertEquals(before, machine.state.value)
    }
    @Test fun notificationStopCancelsOnlyItsOwnGeneration() {
        val machine = SessionMachine()
        val generation = machine.begin("p", 1, 2)
        assertTrue(machine.stop(generation))
        assertEquals(Phase.OFF, machine.state.value.phase)
        assertFalse(machine.connected(generation, 100))
        val stopped = machine.state.value
        assertFalse(machine.stop(generation))
        assertEquals(stopped, machine.state.value)
    }
    @Test fun errorClearsInterfaceFlagAndCannotReconnectItself() {
        val machine = SessionMachine(); val gen = machine.begin("p", 0, 20)
        machine.connected(gen, 0, true); assertTrue(machine.state.value.vpnInterfaceUp)
        machine.fail(gen, "LINK_LOST")
        assertEquals(Phase.ERROR, machine.state.value.phase); assertFalse(machine.state.value.vpnInterfaceUp)
        assertFalse(machine.connected(gen, 0)); assertEquals(0L, machine.state.value.metrics.downBps)
    }
    @Test fun historyBoundedAndMetricsRejectedAfterStop() {
        val machine = SessionMachine(); val gen = machine.begin("p", 0, 2); machine.connected(gen, 0)
        repeat(1000) { machine.metrics(gen, Metrics(it.toLong(), 0, 10, 1, 2, 2)) }
        assertEquals(60, machine.state.value.history.size); assertEquals(940L, machine.state.value.history.first().downBps)
        machine.stop(); assertTrue(machine.state.value.history.isEmpty())
    }
    @Test fun rttUnitsAndOrbitAreStable() {
        assertEquals("999 kbps", Presentation.rate(999_000)); assertEquals("1.0 Mbps", Presentation.rate(1_000_000))
        assertEquals("—", Presentation.rtt(null)); assertEquals(1, Presentation.rttBand(399)); assertEquals(2, Presentation.rttBand(400)); assertEquals(2, Presentation.rttBand(799)); assertEquals(3, Presentation.rttBand(800))
        assertEquals(213f, Presentation.orbit(213f, 100_000_000, 0f))
        val stopped = Presentation.orbit(213f, 10_000_000)
        assertTrue(Presentation.orbit(stopped, 10_000_000) > stopped)
    }

    @Test fun notificationAlwaysShowsBothSpeedsAndRtt() {
        val text = Presentation.notification(SessionSnapshot(metrics = Metrics(downBps = 12_300_000, upBps = 4_500_000, rttMs = 321)))
        assertTrue(text.contains("↓ 12.3 Mbps")); assertTrue(text.contains("↑ 4.5 Mbps")); assertTrue(text.contains("RTT 321 ms"))
    }

    @Test fun fullProfileEditPersistsMetadataAndSecretsTogether() {
        val settings = AppSettings(routing = policy(block = listOf("example.org")))
        val v = editorVault().copy(settings = settings)
        val edit = ProfileEditing.open(v, "local")
        val key = Base64.getEncoder().encodeToString(ByteArray(32) { 42 })
        val draft = edit.initial.copy(name = "Changed", serverId = "abcdefabcdefabcdefabcdefabcdefab", userId = "87654321",
            providerId = "NEW_MAIL", mailboxNamespace = "ITOM/87654321/new", pairId = "fedcba9876543210fedcba9876543210", pskBase64 = key,
            accounts = listOf(edit.initial.accounts.single().copy(id = "renamed_mail", name = "Primary", host = "imap.new.example.invalid", port = "1993",
                username = "new@example.invalid", password = "  NEW_PASSWORD  ")))
        val next = ProfileEditing.apply(v, edit.profileId, edit.revision, draft)
        val loaded = Codec.vault(Codec.encode(next))
        val p = loaded.profiles.single()
        assertEquals("local", p.profileId); assertEquals(v.selectedProfileId, loaded.selectedProfileId)
        assertEquals("87654321", p.userId); assertEquals(draft.serverId, p.serverId); assertEquals("Changed", p.name)
        assertEquals(draft.mailboxNamespace, p.mailboxNamespace); assertEquals("new_mail", p.providerId)
        assertEquals("new_mail", p.accounts.single().providerId); assertEquals(1993, p.accounts.single().port)
        assertEquals("renamed_mail", p.accounts.single().id); assertEquals(draft.accounts.single().host, p.accounts.single().host)
        assertEquals(key, loaded.keys.getValue(p.encryptionKeyRef).pskBase64)
        assertEquals(draft.pairId, loaded.keys.getValue(p.encryptionKeyRef).pairId)
        assertEquals("new@example.invalid", loaded.credentials.getValue(p.accounts.single().credentialRef).username)
        assertEquals("  NEW_PASSWORD  ", loaded.credentials.getValue(p.accounts.single().credentialRef).password)
        assertEquals(v.settings, loaded.settings)
        assertEquals("12345678", v.profiles.single().userId); assertEquals("TEST_ONLY_PASSWORD", v.credentials.values.single().password)
        assertFalse(Codec.exportPublic(p).contains("NEW_PASSWORD")); assertFalse(Codec.exportPublic(p).contains(key))
    }

    @Test fun replacingAccountsRemovesOnlyTheirSecrets() {
        val v = twoProfileVault(); val before = Codec.encode(v); val edit = ProfileEditing.open(v, "local")
        val new = ProfileEditing.newAccount(edit.initial.accounts).copy(username = "added@example.invalid", password = "ADDED_PASSWORD")
        val disabled = ProfileEditing.newAccount(edit.initial.accounts).copy(username = "disabled@example.invalid", password = "DISABLED_PASSWORD", enabled = false)
        val next = ProfileEditing.apply(v, "local", edit.revision, edit.initial.copy(accounts = listOf(new, disabled)))
        assertEquals(2, next.profiles.first { it.profileId == "local" }.totalLines)
        assertFalse("keystore:mail" in next.credentials)
        assertEquals(setOf(new.localRef, disabled.localRef, "keystore:second_mail"), next.credentials.keys)
        assertEquals("OTHER_PASSWORD", next.credentials.getValue("keystore:second_mail").password)
        assertEquals(v.profiles[1], next.profiles[1]); assertSame(v.keys["keystore:second_pair"], next.keys["keystore:second_pair"])
        assertEquals(listOf(true, false), next.profiles.first().accounts.map { it.enabled })
        assertEquals(before, Codec.encode(v))
    }

    @Test fun staleOrDeletedProfileDraftCannotOverwriteVault() {
        val v = editorVault(); val edit = ProfileEditing.open(v, "local")
        val changed = v.copy(revision = v.revision + 1, profiles = listOf(profile().copy(name = "Newer edit")))
        val before = Codec.encode(changed)
        error("PROFILE_EDIT_CONFLICT") { ProfileEditing.apply(changed, "local", edit.revision, edit.initial.copy(name = "Stale edit")) }
        assertEquals(before, Codec.encode(changed))
        error("PROFILE_MISSING") { ProfileEditing.apply(VaultDocument(), "local", edit.revision, edit.initial) }
    }

    @Test fun invalidEditorValuesLeaveSourceVaultIntact() {
        val v = editorVault(); val before = Codec.encode(v); val d = ProfileEditing.open(v, "local").initial; val a = d.accounts.single()
        val invalid = listOf(
            "USER_ID" to d.copy(userId = "01234567"), "PSK_FORMAT" to d.copy(pskBase64 = "abcd"),
            "ACCOUNT_COUNT" to d.copy(accounts = emptyList()), "ACCOUNT_COUNT" to d.copy(accounts = listOf(a.copy(enabled = false))),
            "IMAP_PORT" to d.copy(accounts = listOf(a.copy(port = "65536"))), "IMAP_PORT" to d.copy(accounts = listOf(a.copy(port = "port"))),
            "IMAP_HOST" to d.copy(accounts = listOf(a.copy(host = "https://imap.example.invalid"))),
            "CREDENTIAL_FORMAT" to d.copy(accounts = listOf(a.copy(username = "x\r\nLOGOUT"))),
            "DUPLICATE_ACCOUNT" to d.copy(accounts = listOf(a, a.copy(localRef = "keystore:another"))),
        )
        invalid.forEach { (code, draft) -> error(code) { ProfileEditing.apply(v, "local", v.revision, draft) }; assertEquals(before, Codec.encode(v)) }
    }

    @Test fun editingCannotClaimAnotherServerOrCredentialReference() {
        val v = twoProfileVault(); val edit = ProfileEditing.open(v, "local"); val before = Codec.encode(v)
        error("DUPLICATE_SERVER") { ProfileEditing.apply(v, "local", v.revision, edit.initial.copy(serverId = secondProfile().serverId)) }
        error("VAULT_CREDENTIALS") { ProfileEditing.apply(v, "local", v.revision,
            edit.initial.copy(accounts = listOf(edit.initial.accounts.single().copy(localRef = "keystore:second_mail")))) }
        assertEquals(before, Codec.encode(v))
    }

    @Test fun emptySecretFieldsCannotEraseRealConnectionSecrets() {
        val v = editorVault(); val d = ProfileEditing.open(v, "local").initial
        error("CREDENTIAL_FORMAT") { ProfileEditing.apply(v, "local", v.revision, d.copy(accounts = listOf(d.accounts.single().copy(password = "")))) }
        error("PAIR_ID") { ProfileEditing.apply(v, "local", v.revision, d.copy(pairId = "", pskBase64 = "")) }
        assertEquals("TEST_ONLY_PASSWORD", v.credentials.values.single().password)
    }

    @Test fun incompleteProfileRequiresAllSecretsAndThenBecomesReady() {
        val v = VaultDocument(profiles = listOf(profile()), selectedProfileId = "local", incompleteProfileIds = setOf("local"))
        val old = Codec.vault(Codec.encode(v)); val d = ProfileEditing.open(old, "local").initial
        assertEquals("", d.pskBase64); assertEquals("", d.accounts.single().password)
        error("CREDENTIAL_FORMAT") { ProfileEditing.apply(old, "local", 0, d.copy(name = "Renamed")) }
        val supplied = d.copy(pairId = invitation().pairId, pskBase64 = invitation().pskBase64,
            accounts = listOf(d.accounts.single().copy(username = "user@example.invalid", password = "SUPPLIED_PASSWORD")))
        val saved = Codec.vault(Codec.encode(ProfileEditing.apply(old, "local", 0, supplied)))
        assertTrue(saved.incompleteProfileIds.isEmpty()); assertEquals("SUPPLIED_PASSWORD", saved.credentials.values.single().password)
        assertEquals(supplied, ProfileEditing.open(saved, "local").initial)
        assertEquals(setOf("local"), old.incompleteProfileIds); assertTrue(old.credentials.isEmpty())
    }

    @Test fun incompleteSecretsCannotAliasOrOrphanOtherSecrets() {
        val v = editorVault(); val incomplete = secondProfile()
        val valid = v.copy(profiles = v.profiles + incomplete, incompleteProfileIds = setOf(incomplete.profileId))
        Validation.vault(valid)
        error("VAULT_CREDENTIALS") { Validation.vault(valid.copy(profiles = listOf(profile(), incomplete.copy(accounts = profile().accounts)))) }
        error("VAULT_KEYS") { Validation.vault(valid.copy(profiles = listOf(profile(), incomplete.copy(encryptionKeyRef = profile().encryptionKeyRef)))) }
        error("VAULT_CREDENTIALS") { Validation.vault(valid.copy(credentials = valid.credentials + ("keystore:orphan" to MailCredential("a", "b")))) }
        error("VAULT_KEYS") { Validation.vault(valid.copy(keys = valid.keys + ("keystore:orphan" to PairSecret(invitation().pairId, invitation().pskBase64)))) }
    }

    @Test fun addingAccountDoesNotCopyAnotherAccountsCredentials() {
        val d = ProfileEditing.open(editorVault(), "local").initial
        val added = ProfileEditing.newAccount(d.accounts)
        assertEquals(d.accounts.single().host, added.host); assertEquals("993", added.port)
        assertTrue(added.username.isEmpty()); assertTrue(added.password.isEmpty())
        assertNotEquals(d.accounts.single().localRef, added.localRef); assertNotEquals(added.localRef, ProfileEditing.newAccount(d.accounts).localRef)
        assertEquals("TEST_ONLY_PASSWORD", d.accounts.single().password)
    }

    @Test fun editorDraftDiagnosticsNeverPrintSecretMaterial() {
        val edit = ProfileEditing.open(editorVault(), "local")
        listOf(edit.toString(), edit.initial.toString(), edit.initial.accounts.single().toString()).forEach {
            assertFalse(it.contains("TEST_ONLY_PASSWORD")); assertFalse(it.contains(invitation().pskBase64)); assertFalse(it.contains("test@example.invalid"))
        }
    }

    @Test fun socksSettingsMigrationAndEncryptedDocumentRoundtrip() {
        val original = editorVault()
        val old = Codec.json.encodeToJsonElement(original).jsonObject.toMutableMap()
        old["settings"] = JsonObject(old.getValue("settings").jsonObject.toMutableMap().apply {
            put("schema_version", JsonPrimitive(3)); remove("socks")
        })
        old.remove("socksCredential")
        val decoded = Codec.decodeVault(JsonObject(old).toString())
        assertTrue(decoded.migrated); assertEquals(1080, decoded.document.settings.socks.port)
        assertEquals(original.profiles, decoded.document.profiles)
        val withAuth = original.copy(settings = original.settings.copy(socks = SocksSettings(23456, true)),
            socksCredential = SocksCredential("local-user", "secret-pass"))
        val restored = Codec.vault(Codec.encode(withAuth))
        assertEquals(23456, restored.settings.socks.port)
        assertEquals("secret-pass", restored.socksCredential?.password)
        assertFalse(withAuth.toString().contains("secret-pass"))
        assertFalse(PublicConfig(settings = restored.settings).toString().contains("secret-pass"))
    }
    @Test fun socksPortAndAuthenticationValidation() {
        val v = editorVault()
        for (port in listOf(0, 1023, 65536)) error("SOCKS_PORT") { Validation.settings(v.settings.copy(socks = SocksSettings(port))) }
        error("SOCKS_CREDENTIALS") { Validation.vault(v.copy(settings = v.settings.copy(socks = SocksSettings(1080, true)))) }
        error("SOCKS_CREDENTIALS") { Validation.vault(v.copy(socksCredential = SocksCredential("user", "я".repeat(128)))) }
        Validation.vault(v.copy(settings = v.settings.copy(socks = SocksSettings(65535, true)), socksCredential = SocksCredential("user", "я".repeat(127))))
    }
    @Test fun transportSampleAndCountersAreOneGeneration() {
        val machine = SessionMachine()
        val old = machine.begin("old", 1, 20)
        val current = machine.begin("current", 2, 20)
        assertFalse(machine.sample(old, Metrics(activeLines=20,totalLines=20), "old-ready", 1))
        assertTrue(machine.sample(current, Metrics(activeLines=14,totalLines=20), "same-14", 2))
        val snapshot=machine.state.value
        assertEquals(14, snapshot.metrics.activeLines)
        assertEquals("same-14", snapshot.transport)
        assertEquals(2L, snapshot.sampledAt)
        machine.fail(current,"TRANSPORT_FAILED")
        assertEquals(0, machine.state.value.metrics.activeLines)
        assertEquals("{}", machine.state.value.transport)
        assertNull(machine.state.value.sampledAt)
        assertFalse(machine.sample(current, Metrics(activeLines=20,totalLines=20), "late-ready", 3))
    }
}
