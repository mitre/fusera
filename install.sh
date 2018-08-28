#!bin/bash

curl -s -L -o /usr/local/bin/fusera https://github.com/mitre/fusera/releases/download/v0.0.12/fusera
curl -s -L -o /usr/local/bin/sracp https://github.com/mitre/fusera/releases/download/v0.0.12/sracp

chmod +x /usr/local/bin/fusera
chmod +x /usr/local/bin/sracp
