# Getting Started with Atlas

*A plain-language guide — no technical background needed.*

## What is Atlas?

Atlas watches a list of stocks/ETFs you choose, and for each one it tells you whether now looks
like a good moment to **buy, add more, hold, trim, sell, or wait** — along with a plain-English
reason why.

**Atlas never buys or sells anything for you.** It has no connection to any broker and cannot
place trades. It only gives you information to help you decide. You always click the button in
your own broker's app yourself.

**Important:** Atlas is a hobby/research project, not a financial product. It is not financial
advice, and any decision you make based on it is entirely your own responsibility. Its prices come
from Yahoo Finance's public data, which is not an official or guaranteed source and can occasionally
be wrong, delayed, or unavailable. The project's code is open and free to inspect or reuse.

## Opening Atlas

Atlas runs on your own computer. Open http://localhost:8080 in any browser (Chrome, Safari,
Firefox, Edge — all work). There is no login: Atlas is only reachable from the computer it runs
on, which is what keeps your data private — don't make it reachable from anywhere else.

## The pages, in plain terms

You'll see a menu at the top with these pages:

- **Dashboard** — your home screen. Shows what you currently own, how it's doing, and a summary
  of anything that needs your attention.
- **Portfolio** — where you tell Atlas what you actually own, and see your full trade history.
- **Watchlist** — the list of stocks/ETFs you want Atlas to keep an eye on, whether or not you own
  them yet.
- **Recommendations** — pick any single stock and get an instant, detailed opinion on it.
- **Alerts** — a list of everything on your watchlist that currently isn't a plain "hold" — i.e.
  the things worth a second look right now.
- **Backtests** — "if I had followed Atlas's advice on this stock over the past months, how would
  that have gone?" A history check, not a promise about the future.
- **Settings** — your portfolios, the ML model settings, and (further down) some advanced tools
  covered later in this guide.

## Step 1 — Add stocks to your watchlist

Go to **Watchlist** → type a ticker symbol (e.g. `AAPL` for Apple, `MSFT` for Microsoft) → **Add**.
You don't need to own something to watch it. Atlas will start tracking its price history and
computing indicators for it right away.

## Step 2 — Tell Atlas what you already own

This step matters: Atlas gives smarter advice when it knows what's actually in your portfolio
(e.g. it can tell the difference between "worth buying for the first time" and "worth adding to
what you already hold").

You can keep more than one portfolio — e.g. "Main" and "Speculative" — and switch between them
using the tabs at the top of the Dashboard and Portfolio pages. Everyone starts with one; add,
rename, or delete portfolios from the bottom of the **Settings** page (up to 5). Recommendations
still consider anything you hold in *any* of your portfolios, so a stock you already own in one
never gets suggested as a fresh buy just because a different portfolio tab is open.

Go to **Portfolio** → scroll to **Record a transaction** → fill in:

- **Symbol** — pick from the dropdown (only shows symbols you're already watching — add it to
  your watchlist first if it's missing)
- **Type** — Buy or Sell
- **Trade date** — the day you actually traded (today by default). For a EUR or USD trade, Atlas
  uses that day's official exchange rate, so enter the real date for older trades
- **Quantity** — how many shares/units
- **Unit price** — the price *per share*, not the total amount you paid
- **Currency** — PLN, EUR, or USD (Atlas converts everything to PLN internally so your totals
  always add up, no matter what currency each trade was in)

Click **Record transaction**. Your position and gains/losses update immediately. Clicking twice,
or resubmitting after a connection hiccup, still records the trade only once.

Made a mistake? Delete the wrong transaction in **Transaction History** (each row has a
**Remove** button, and checkboxes remove several at once) — the position is recalculated from
what remains. Sold something? Record a **Sell** rather than deleting anything.

Positions also have a **Hide** button. It clears the position from the table but keeps its
transactions, so the next trade you record for that symbol brings it back, rebuilt from the whole
history. Use it only to tidy up; to change what you own, record a trade or delete one.

Under each current price, a small note says where the price comes from: a date for a current
market price, **stale quote** when the market price is several days old, or **last trade price**
when Atlas has no market price yet and is showing what you paid.

## Step 3 — Read a recommendation

Go to **Recommendations**, pick a symbol, and you'll get one of:

| Term | Plain meaning |
|---|---|
| **BUY** | Good moment to start a new position — you don't currently hold this one |
| **ACCUMULATE** | Good moment to add to what you already hold |
| **HOLD** | No strong signal either way — sit tight |
| **WAIT** | A signal is forming but isn't strong/confident enough to act on yet |
| **REDUCE** | Consider trimming part of an existing position |
| **SELL** | Consider exiting a position entirely |

Every recommendation comes with a **confidence** percentage (how clear-cut the signal is, not
how likely a gain is) and a short list of *why* — which signals (price momentum, market mood,
your own portfolio's risk level, etc.) drove that call. If
you disagree or don't understand a reason, that's worth reading closely before acting on it —
Atlas explains itself on purpose so you never have to just take its word for it. An **incomplete
inputs** badge means some information was missing when it was calculated (for example no current
price for something you own), so treat that one with extra caution.

## Checking in day to day

- **Alerts** is the fastest way to see "anything I should look at today?" — it only shows symbols
  that currently aren't a plain HOLD.
- **Dashboard** shows your live profit/loss, updated with current market prices, plus a summary
  count of alerts.
- Prices and recommendations refresh automatically in the background every few minutes — you
  don't need to keep refreshing the page yourself.

## Backing up and starting fresh (Settings page)

Scroll to the bottom of the **Settings** page for three advanced tools:

- **Download backup** — saves everything (your positions, trade history, watchlist, settings) to
  one file on your computer. Do this occasionally, and always right before using the next tool.
  Keep a copy somewhere other than this computer too: a backup on the same disk doesn't survive
  losing that disk.
- **Restore from backup** — loads a backup file back in, replacing everything currently in Atlas. Backups
  from older versions of Atlas restore too.
- **Flush all data** — permanently erases everything and returns Atlas to a completely blank
  slate. This button stays **locked** and greyed out unless you've downloaded a backup within the
  last 5 minutes — that's deliberate, so you can never accidentally wipe your only copy of the
  data.

Only use Flush if you genuinely want to start over from zero.

## Light/dark mode and language

The top-right corner of every page has a **Theme** menu (Light / Dark / follow your device's
setting) and a **Language** menu (English, Polish, German, French, Spanish). Both take effect
immediately and are remembered next time you visit.

## If something looks wrong

Every number Atlas shows is computed from real data — nothing is made up or simulated. If a price
or recommendation looks stale, it will usually catch up within a few minutes on its own (Atlas
refreshes in the background). If something still looks genuinely wrong, don't assume it's
expected: the application log (`docker compose logs atlas-api`) usually says why.
