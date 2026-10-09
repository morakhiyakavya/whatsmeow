# Copy to .env.local.ps1, enter your local connection settings, and dot-source it:
# . ./.env.local.ps1
# Percent-encode special characters in a URL password. Use sslmode=verify-full
# and trusted certificates for a remote server; disable below is for localhost.
$env:WHATSAPP_DATABASE_URL = 'postgresql://ipo_app:REPLACE_PASSWORD@127.0.0.1:5432/ipo_app?sslmode=disable'
