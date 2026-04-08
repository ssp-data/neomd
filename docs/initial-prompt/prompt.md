# neomd
I love neovim! I love RSS reader like newsboat (dotfiles...

and general TUIs, I use a lot of them.

See my configs in dotfiles (its with stow, so there hidden files) - e.g. check out my customization and shortcuts for neomutt in) - e.g. check out my customization and shortcuts for neomutt in /home/sspaeti/git/general/dotfiles/mutt - I even built a screener that was in HEY.com/ which i like a lot and which is my client I use: https://www.hey.com/features/the-screener/ - there's also a CLI now see /home/sspaeti/git/email/hey-cli


On hackernews I just saw this: https://www.emailmd.dev/, sending email as markdown.


That made me thinking, how hard would it be, to have similar experience with neomutt, but much simpler for some key features - to send and view emails that are configured via IMAP or similar?


With all the dotfiles available and code from HEY cli, newsboat or neomutt, how would I build something similar to send email with neovim and read too, everything based on Markdown?

Maybe we can also use https://github.com/kepano/defuddle (code here: /home/sspaeti/git/email/defuddle) that converts any HTML into Markdown and then we have emailmd.dev to convert MD to HTML.


TO be hontest, I don't even want to send HTML, i just want to send simple text. E.g. in my newsletter I have this template which is simple and I like (/home/sspaeti/git/sspaeti.com/listmonk/misc/email-template.html) so maybe we can make a very simple template, or none at all, just plain markdown converted to plain text.

but having links and headers would be nice still, or bold, italic etc. some basic fomrattings.


Please research what's the easiest way to build a terminal based email client that works with neovim and markdown similar to neomutt, but simple, built from scratch - use any library that's useful such as crush to make beautiful, or Ratatui to create terminal UIs (don't create everything from scratch, just the summarized tool).

I guess prefered languages are Rust or Go (e.g. /home/sspaeti/git/email/msgvault was recently built with Go and is also to do with email, also hey-cli is written in go).

Also use https://github.com/charmbracelet/gum for making the CLI or TUI nice, similar to or https://github.com/charmbracelet/glamour to make render Markdown in the terminal.

Speaking of aestetics, there's also pop email for go: https://github.com/charmbracelet/pop



## Updates/additions #1

1. i added neomutt code /home/sspaeti/git/email/neomutt, it's in C but it can help if we need to solve something complex or as they probably have solved all ways of email. 

2. for offline availablility neomutt used /home/sspaeti/git/general/dotfiles/mutt/.msmtprc (i blieve??) which could be used, but don't have too, at least it seems to work. but if there's a simpler or direct go implementation, even better. but offline support is something that would be great too, at some point. But IMAP is also ok to start without offline. 

3. can you prepare the architecture to have the HEY Screener that only allows emails in inbox that are approved in a screened_in.txt list e.g. all the code in bash and for different folder such as inbox (screened in only) screened out, papertrail for receipts etc and feed for newsletter etc. all code is in /home/sspaeti/git/general/dotfiles/mutt/.config/mutt. can we if not yet implement, at least


## Created Claude Plan
/home/sspaeti/.claude/plans/giggly-juggling-hinton.md
[plan](prompt-plan.md)



