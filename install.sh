#!bin/bash

curl -s -L -o /usr/local/bin/fusera https://github.com/mitre/fusera/releases/download/v2.0.0/fusera
curl -s -L -o /usr/local/bin/sracp https://github.com/mitre/fusera/releases/download/v2.0.0/sracp

chmod +x /usr/local/bin/fusera
chmod +x /usr/local/bin/sracp
