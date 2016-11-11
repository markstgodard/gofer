curl -s -i \
  -H "Content-Type: application/json" \
  -d '
{ "auth": {
    "identity": {
      "methods": ["password"],
      "password": {
        "user": {
          "name": "cf",
          "domain": { "id": "default" },
          "password": "secret"
        }
      }
    }
  }
}' \
  http://192.168.56.101:5000/v3/auth/tokens |grep X-Subject | cut -d' ' -f2
