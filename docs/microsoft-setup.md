# Connect a Microsoft mailbox (outlook.com, live.in, hotmail.com)

Microsoft stopped accepting app passwords for personal mailboxes from third-party apps
in September 2024. Dzinga therefore signs in with Microsoft's own sign-in page (OAuth)
and asks for **read-only** mail access (`Mail.Read`). It needs an "app registration"
that you own. It's free and takes about 5 minutes, once.

## 1. Register the app

1. Go to **https://portal.azure.com** and sign in with the account that owns your Azure
   subscription. A personal account without a directory can't open entra.microsoft.com
   ("account does not exist in tenant 'Microsoft Services'"), so create a free Azure
   account first.
2. Open **App registrations** and choose **New registration**.
   - **Name:** `Dzinga (local)`
   - **Supported account types:** *Personal Microsoft accounts only*
   - **Redirect URI:** leave empty
   - Choose **Register**.
3. On the app's **Overview** page, copy the **Application (client) ID**. It's a GUID
   such as `1a2b3c4d-…`. It identifies the app, it isn't a secret, and there is no
   client secret to create.
4. Open **Authentication**, set **Allow public client flows** to **Yes**, and choose
   **Save**.
5. Open **API permissions**. `User.Read` is there by default. Choose
   **Add a permission** → **Microsoft Graph** → **Delegated** → **Mail.Read** →
   **Add**. Don't add any write permissions.

> If the portal says you need a directory or subscription before you can register an
> app, stop there. Microsoft has started requiring this for some personal accounts,
> and there are other routes we can take.

## 2. Tell Dzinga the client ID

Add this line to `.env.local` in the repo root. That file is git-ignored.

```
DZ_MS_CLIENT_ID=<your Application (client) ID>
```

## 3. Run it

```bash
make preview-outlook EMAIL=you@live.in SINCE=2026-08-14   # dry run
make ingest-outlook  EMAIL=you@live.in SINCE=2026-08-14   # import
```

The importer prints a URL and a short code. Open the URL, enter the code, sign in, and
approve. The access token stays in memory for that run only and is never written to
disk. If a different account signs in, the run stops.

To revoke access later, go to **https://account.live.com/consent/Manage** and remove
"Dzinga (local)".
