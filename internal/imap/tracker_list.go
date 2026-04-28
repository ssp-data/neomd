// Package imap — email spy pixel / open-tracker blocklist.
//
// This file is a curated denylist (a.k.a. blocklist) of URL patterns used by
// known email service providers (ESPs) and standalone tracking tools to
// detect when a recipient opens an email. The pattern is the same for all
// of them: the sender embeds a 1×1 transparent image whose URL encodes a
// per-recipient token; loading that image tells the sender you opened the
// message — plus your IP, user-agent, rough location, time, etc.
//
// We use the term "denylist" (HEY calls it a "spy tracker list"). Note that
// "whitelist/blacklist" is the wrong framing for this: we are *blocking*
// these patterns, so it's a denylist, not an allowlist.
//
// Sources merged into this list:
//
//   - DHH / HEY original list of "spy pixels named'n'shamed"
//     https://gist.github.com/dhh/360f4dc7ddbce786f8e82b97cdad9d20
//     License: MIT
//
//   - Simplify Gmail tracker list (Michael Leggett, ex-Gmail design lead),
//     200+ trackers, the most actively maintained list in the wild. Used
//     by MailTrackerBlocker and Twobird as well.
//     https://github.com/leggett/simplify-trackers
//     License: BSD-3-Clause
//     Copyright (c) 2021 Michael Leggett, simpl.fyi
//
//   - LeaveMeAlone email-trackers list, derived from UglyEmail, written
//     in adblock-filter syntax.
//     https://github.com/leavemealone-app/email-trackers
//     License: CC-BY 3.0
//
// Detection strategy (matches HEY's two-layer approach):
//   1. KnownTrackerPatterns + KnownTrackers: high-confidence substring/host
//      matches against well-known ESPs. When one matches, we know exactly
//      who the sender is using and can label it ("Tracked by Mailchimp").
//   2. Generic 1×1 pixel heuristic (kept in client.go, not here): catches
//      branded/custom tracking domains we don't have on the list.
//
// Together those two cover the ~98% HEY claims for their service.
//
// Pattern style: each entry is a substring, designed for strings.Contains
// against the lowercased image src URL. Pick the most specific portion of
// the URL — the path or subdomain that won't collide with a legitimate
// asset URL on the same domain. Avoid bare TLDs.

package imap

import "strings"

// TrackerService describes one ESP or tracking tool and the URL fragments
// its open-tracking pixels use. A single service may use multiple patterns
// (e.g. Hubspot uses several subdomain variants).
type TrackerService struct {
	// Name is the human-readable provider name shown in the UI
	// when we strip a pixel ("Tracked by Mailchimp").
	Name string

	// Patterns are URL substrings; if any one is found in an
	// image src (case-insensitive), it's flagged as that service.
	Patterns []string
}

// KnownTrackers is the structured denylist: 150+ services with URL patterns.
// Generated from the Simplify Gmail tracker list (leggett/simplify-trackers,
// BSD-3-Clause) cross-checked with LeaveMeAlone and DHH's original HEY list.
//
// Ordering: alphabetical by service name. The generic 1×1 pixel heuristic
// in client.go runs AFTER this list, so we attribute to a known service first.
//
// Last synced: 2026-04-28
var KnownTrackers = []TrackerService{
	{"365offers", []string{"trk.365offers.trade"}},
	{"Absolutesoftware", []string{"click.absolutesoftware-email.com/open.aspx"}},
	{"ActionKit", []string{"track.sp.actionkit.com/q/"}},
	{"Acoustic", []string{"mkt.com/open", "mkt.net/open"}},
	{"ActiveCampaign", []string{"lt.php?l=open", "lt.php?tid=", "/lt.php?"}},
	{"Active.com", []string{"click.email.active.com/q"}},
	{"Adobe", []string{"demdex.net", "t.info.adobesystems.com", "toutapp.com", "/trk?t=", "sparkpostmail2.com"}},
	{"AgileCRM", []string{"agle2.me/open"}},
	{"Airbnb", []string{"email.airbnb.com/wf/open"}},
	{"AirMiles", []string{"email.airmiles.ca/O"}},
	{"Alaska Airlines", []string{"click.points-mail.com/open", "sjv.io/i/", "gqco.net/i/"}},
	{"Amazon", []string{"awstrack.me", "aws-track-email-open", "/gp/r.html", "/gp/forum/email/tracking", "amazonappservices.com/trk", "amazonappservices.com/r/", "awscloud.com/trk"}},
	{"Apple", []string{"apple.com/report/2/its_mail_sf", "apple_email_link/spacer"}},
	{"Appriver", []string{"appriver.com/e1t/o/"}},
	{"Asus", []string{"emditpison.asus.com"}},
	{"AWeber", []string{"openrate.aweber.com"}},
	{"Axios", []string{"link.axios.com/img/"}},
	{"Bananatag", []string{"bl-1.com"}},
	{"Blueshift", []string{"blueshiftmail.com/wf/open", "getblueshift.com/track"}},
	{"Bombcom", []string{"bixel.io"}},
	{"Boomerang", []string{"mailstat.us/tr"}},
	{"Boots", []string{"boots.com/rts/open.aspx"}},
	{"Boxbe", []string{"boxbe.com/stfopen"}},
	{"Browserstack", []string{"browserstack.com/images/mail/track-open"}},
	{"BuzzStream", []string{"tx.buzzstream.com"}},
	{"Campaign Monitor", []string{"cmail1.com/t/", "cmail2.com/t/", "cmail3.com/t/", "cmail4.com/t/", "cmail5.com/t/", "cmail10.com/t/", "cmail19.com/t/", "cmail20.com/t/", "createsend1.com/t/"}},
	{"Canary Mail", []string{"canarymail.io/track", "pixels.canarymail.io"}},
	{"Cirrus Insight", []string{"tracking.cirrusinsight.com", "pardot.com/r/"}},
	{"Clio", []string{"market.clio.com/trk"}},
	{"Close", []string{"close.io/email_opened", "close.com/email_opened", "dripemail2"}},
	{"CloudHQ", []string{"cloudhq.io/mail_track", "cloudhq-mkt.net/mail_track"}},
	{"Coda", []string{"coda.io/logging/ping"}},
	{"CodePen", []string{"mailer.codepen.io/q"}},
	{"ConneQuityMailer", []string{"connequitymailer.com/open/"}},
	{"Constant Contact", []string{"rs6.net/on.jsp", "constantcontact.com/images/p1x1.gif"}},
	{"ContactMonkey", []string{"contactmonkey.com/api/v1/tracker"}},
	{"ConvertKit", []string{"open.convertkit-mail.com", "convertkit-mail.com/o/", "convertkit-mail2.com/o/", "convertkit-mail3.com/o/"}},
	{"Copper", []string{"prosperworks.com/tp/t"}},
	{"Cprpt", []string{"/o.aspx?t="}},
	{"Critical Impact", []string{"portal.criticalimpact.com/c2/"}},
	{"Customer.io", []string{"customeriomail.com/e/o", "track.customer.io/e/o"}},
	{"Dell", []string{"ind.dell.com/wf/open"}},
	{"DidTheyReadIt", []string{"xpostmail.com/t/", "didtheyreadit.com"}},
	{"DotDigital", []string{"trackedlink.net/", "dmtrk.net/open"}},
	{"Driftem", []string{"dfrnt.com/o"}},
	{"Dropbox", []string{"dropbox.com/l/"}},
	{"DZone", []string{"mailer.dzone.com/open.php"}},
	{"Ebsta", []string{"ebsta.com/r/", "ebsta.gif"}},
	{"Emarsys", []string{"emarsys.com/e2t/o/"}},
	{"Etransmail", []string{"clicks.em.etransmail.com/open/log/"}},
	{"EventBrite", []string{"eventbrite.com/emails/action"}},
	{"EveryAction", []string{"click.everyaction.com/j/"}},
	{"Evite", []string{"mta.evite.com/imp"}},
	{"Facebook", []string{"facebook.com/email/open_tracking", "facebook.com/tr/"}},
	{"Flipkart", []string{"flipkart.com/dynip/image.php"}},
	{"ForMirror", []string{"formirror.com/open/"}},
	{"Freelancer", []string{"freelancer.com/users/notifications/check/"}},
	{"FreshMail", []string{"freshmail.com/external/"}},
	{"Front", []string{"app.frontapp.com/oc/", "web.frontapp.com/oc/"}},
	{"FullContact", []string{"fullcontact.com/wf/open"}},
	{"Gem", []string{"zen.sr/o"}},
	{"GetBase", []string{"getbase.com/e1t/o/"}},
	{"GetMailSpring", []string{"getmailspring.com/open"}},
	{"GetNotify", []string{"email81.com/case"}},
	{"GetPocket", []string{"getpocket.com/s"}},
	{"GetResponse", []string{"getresponse.com/open.html"}},
	{"GitHub", []string{"github.com/notifications/beacon/"}},
	{"Glassdoor", []string{"mail.glassdoor.com/pub/as"}},
	{"GMass", []string{"track.gmass.co", "x.gmtrack.net", "gmass.co/r/"}},
	{"Gmelius", []string{"gml.email/"}},
	{"Google", []string{"google.com/appserve/mkt/img/", "ad.doubleclick.net/ddm/ad/", "google-analytics.com/collect"}},
	{"Grammarly", []string{"grammarly.com/open"}},
	{"Granicus", []string{"govdelivery.com/abe/r/"}},
	{"GrowthDot", []string{"growthdot.com/api/mail-tracking"}},
	{"HomeAway", []string{"sp.trk.homeaway.com/q/"}},
	{"HubSpot", []string{"t.hubspotemail.net", "t.hubspotfree.net", "t.signaux.co", "t.signauxtrois.com", "t.senal.co", "t.sidekickopen", "t.sigopn.co", "t.hsms06.com", "track.hubspot.com"}},
	{"Hunter", []string{"hunter.io/pixel", "mlnk.io/o/"}},
	{"iContact", []string{"click.icptrack.com/icp"}},
	{"Infusionsoft", []string{"infusionsoft.com/app/linkClick/", "infusionsoft.com/t/"}},
	{"Insightly", []string{"insightlytracking.com/b/"}},
	{"Intercom", []string{"via.intercom.io/o", "intercom-mail.com/via/o", "via.intercom-mail.com"}},
	{"JangoMail", []string{"jangomail.com/t/"}},
	{"Klaviyo", []string{"trk.klclick.com", "trk.klclick1.com", "trk.klclick2.com"}},
	{"LaunchBit", []string{"launchbit.com/taz-pixel"}},
	{"LinkedIn", []string{"linkedin.com/emimp/"}},
	{"Litmus", []string{"emltrk.com"}},
	{"LogDNA", []string{"logdna.com/l/"}},
	{"Magento", []string{"/pub/magento-"}},
	{"Mailbutler", []string{"bowtie.mailbutler.io/tracking/hit"}},
	{"Mailcastr", []string{"mailcastr.com/image/"}},
	{"Mailchimp", []string{"list-manage.com/track"}},
	{"MailCoral", []string{"mailcoral.com/open"}},
	{"Mailgun", []string{"email.mailgun.net/o/", "email.mg.", "/o/eJw", "track.mailgun.org"}},
	{"MailInifinity", []string{"mailinifinity.com/ptrack/"}},
	{"Mailjet", []string{"mjt.lu/oo"}},
	{"MailTag", []string{"mailtag.io/email-event"}},
	{"MailTrack", []string{"mailtrack.io/trace", "mltrk.io/pixel"}},
	{"Mailzter", []string{"mailzter.in/webversion"}},
	{"Mandrill", []string{"mandrillapp.com/track"}},
	{"Marketo", []string{"resources.marketo.com/trk", "marketo.com/trk"}},
	{"Mention", []string{"mention.com/e/o/"}},
	{"MetaData", []string{"metadata.io/e1t/o/"}},
	{"MixMax", []string{"email.mixmax.com", "track.mixmax.com"}},
	{"Mixpanel", []string{"api.mixpanel.com/track"}},
	{"MyEmma", []string{"e2ma.net/track/"}},
	{"Nation Builder", []string{"nationbuilder.com/r/o"}},
	{"NeteCart", []string{"netecart.com/lmtracker.aspx"}},
	{"NetHunt", []string{"nethunt.co/api/", "nethunt.com/api/v1/track/email"}},
	{"Newton", []string{"tr.cloudmagic.com"}},
	{"OpenBracket", []string{"openbracket.co/track/"}},
	{"Oracle", []string{"tags.bluekai.com/site", "en25.com/e/o/", "bfrnt.com/o/"}},
	{"Outreach", []string{"outrch.com/api/mailings/opened", "/api/mailings/opened"}},
	{"PayBack", []string{"email.payback.in/a/"}},
	{"PayPal", []string{"paypal-communication.com/O/"}},
	{"Paytm", []string{"trk.paytm.com"}},
	{"phpList", []string{"phplist.com/lists/ut.php", "/lists/ut.php"}},
	{"PipeDrive", []string{"api-mail.pipedrive.com/wf/open"}},
	{"Polymail", []string{"polymail.io/v2/z", "polymail.io/track"}},
	{"Postmark", []string{"pstmrk.it/open", "pstmrk.it/o/"}},
	{"ProductHunt", []string{"producthunt.com/emails/"}},
	{"ProlificMail", []string{"prolificmail.com/lm/"}},
	{"Quora", []string{"quora.com/qemail/mark_read"}},
	{"Rebump", []string{"rebump.cc/api/track"}},
	{"ReplyCal", []string{"replycal.com/tracking/"}},
	{"Return Path", []string{"returnpath.net/pixel.gif"}},
	{"Rocketbolt", []string{"email.rocketbolt.com/o/"}},
	{"Sailthru", []string{"sailthru.com/trk"}},
	{"Salesforce", []string{"nova.collect.igodigital.com", "go.pardot.com/l/", "exct.net/open.aspx"}},
	{"SalesHandy", []string{"saleshandy.com/web/email/countopened"}},
	{"SalesLoft", []string{"salesloft.com/email_trackers"}},
	{"Segment", []string{"email.segment.com/e/o/"}},
	{"Selligent", []string{"strongview.com/t"}},
	{"SendGrid", []string{"/wf/open?upn=", "/wf/open?", "sendgrid.net/wf/open"}},
	{"Sendinblue / Brevo", []string{"sendibt1.com", "sendibt2.com", "sendibt3.com", "sendibt4.com", "sendibm1.com", "sendibm2.com", "sendibm3.com", "sendibw2.com/track/"}},
	{"SendPulse", []string{"stat-pulse.com/open/"}},
	{"Sendy", []string{"/sendy/t/", "/l.php?i="}},
	{"Signal", []string{"signl.live/tracker/open/"}},
	{"Skillsoft", []string{"skillsoft.com/trk"}},
	{"Snov.io", []string{"sgndrp.online/open", "signaldomn.online/track"}},
	{"Sparkloop", []string{"sparkloop.app/open/"}},
	{"Streak", []string{"mailfoogae.appspot.com"}},
	{"Substack", []string{"email.substack.com/o", "mailgun.substack.com", ".substack.com/o/"}},
	{"Superhuman", []string{"r.superhuman.com"}},
	{"TataDocomoBusiness", []string{"tatadocomo.com/TataDocomoBusiness/"}},
	{"Techgig", []string{"mailer.techgig.com"}},
	{"TheAtlantic", []string{"links.e.theatlantic.com/open/log/"}},
	{"TheTopInbox", []string{"thetopinbox.com/track/"}},
	{"Thunderhead", []string{"na5.thunderhead.com"}},
	{"TinyLetter", []string{"tinyletterapp.com"}},
	{"TrackApp", []string{"trackapp.io/b/"}},
	{"Transferwise", []string{"wise.com/track/"}},
	{"Trello", []string{"trello.com/e/"}},
	{"Udacity", []string{"udacity.com/api/"}},
	{"Unsplash", []string{"unsplash.com/email_opened"}},
	{"Upwork", []string{"upwork.com/ab/account-security/"}},
	{"Vcommission", []string{"tracking.vcommission.com"}},
	{"Vtiger", []string{"od1.vtiger.com/shorturl/image/"}},
	{"WildApricot", []string{"wildapricot.com/o/"}},
	{"Wix", []string{"shoutout.wix.com/so/pixel"}},
	{"Workona", []string{"workona.com/mk/op/"}},
	{"YAMM", []string{"yamm-track.appspot"}},
	{"Yesware", []string{"t.yesware.com", "/track/open", "/open.aspx?tp="}},
	{"Zendesk", []string{"futuresimple.com/api/v1/sprite.png"}},
}

// KnownTrackerPatterns is the flat substring list, the form your existing
// code already uses with strings.Contains. Every Pattern from KnownTrackers
// above is included here. Build it once at init() to avoid drift.
//
// Generic path patterns at the top come from the LeaveMeAlone list — these
// are common enough across self-hosted ESPs that a path-only match is safe.
var KnownTrackerPatterns = buildTrackerPatterns()

func buildTrackerPatterns() []string {
	// Generic path-only patterns. These are deliberately specific enough
	// not to false-positive on legitimate URLs (no bare "/open" or "/o").
	patterns := []string{
		"/wf/open",        // SendGrid (also some self-hosted)
		"/track/open.php", // many self-hosted ESPs
		"/ut.php",         // phpList and forks
		"/o.gif",          // common open-tracking filename
		"/pixel.gif",      // common open-tracking filename
		"/beacon",         // generic beacon endpoint
		"/email_opened",   // Close.io and clones
	}

	for _, t := range KnownTrackers {
		patterns = append(patterns, t.Patterns...)
	}
	return patterns
}

// IdentifyTracker returns the service name that owns the given URL, or "".
// Use this when you want to surface attribution to the user. Pass the
// lowercased URL (or do strings.ToLower inside; matching here is case-
// sensitive against pre-lowercased patterns).
//
// Example UI text: "🕵 Tracked by Mailchimp — pixel removed"
func IdentifyTracker(lowercasedURL string) string {
	for _, t := range KnownTrackers {
		for _, p := range t.Patterns {
			if containsCI(lowercasedURL, p) {
				return t.Name
			}
		}
	}
	return ""
}

// containsCI checks if haystack contains needle, case-insensitive.
func containsCI(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}
