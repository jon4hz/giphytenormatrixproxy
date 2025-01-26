# GiphyTenor Matrix Proxy

A Matrix media proxy server that enables easy integration of Giphy and Tenor GIFs into Matrix clients. Based on [maunium/stickerpicker](https://github.com/maunium/stickerpicker/), this proxy implements the Matrix media API to serve GIFs from Giphy and Tenor while maintaining compatibility with Matrix clients.

## Features

- Serves as a Matrix media server for Giphy and Tenor GIFs
- Implements Matrix federation API for seamless integration
- Built-in web interface for serving a GIF search and selection widget
- Supports MSC3860/MSC3916 media download redirects
- Docker support with Traefik integration

## Prerequisites

- Docker and Docker Compose
- Traefik (for reverse proxy and SSL)
- A domain name with DNS configured
- Giphy API key (get one at https://developers.giphy.com/)
- Tenor API key (get one at https://tenor.com/developer/dashboard)

## Installation

1. Clone this repository:
```bash
git clone https://github.com/yourusername/giphytenormatrixproxy.git
cd giphytenormatrixproxy
```

2. Generate a server signing key:
```bash
docker build -t giphytenormatrixproxy giphytenormatrixproxy
docker run --rm giphytenormatrixproxy -generate-key
```

3. Create your configuration file:
```bash
cp example-config.yaml config.yaml
```

4. Edit `config.yaml` with your settings:
- Set your `server_name` (e.g., `giphy.example.com`)
- Add your generated server key
- Insert your Giphy and Tenor API keys
- Configure other options as needed

5. Configure environment variables:
```bash
cp .env.example .env
```

6. Edit `.env` with your settings:
```env
MATRIX_MEDIA_DOMAIN=mediaproxy.example.com
NETWORK_NAME=matrix
CONFIG_PATH=./config.yaml
```

## Docker Compose Setup with Traefik

Example docker-compose configuration using Traefik:

```yaml
services:
  giphytenormatrixproxy:
    image: ${PROXY_IMAGE:-giphytenormatrixproxy}
    container_name: ${CONTAINER_NAME:-giphytenormatrixproxy}
    build:
      context: ${BUILD_CONTEXT:-./matrix/build/giphytenormatrixproxy}
      dockerfile: Dockerfile
    environment:
      TZ: ${TIMEZONE:-UTC}
    networks:
      - ${NETWORK_NAME:-matrix}
    volumes:
      - ${CONFIG_PATH:-./matrix/giphytenormatrixproxy/config.yaml}:/data/config.yaml
    restart: on-failure
    labels:
      traefik.enable: true
      traefik.http.routers.${CONTAINER_NAME:-giphytenormatrixproxy}.rule: Host(`${MATRIX_MEDIA_DOMAIN}`)
      traefik.http.routers.${CONTAINER_NAME:-giphytenormatrixproxy}.entrypoints: websecure
      traefik.http.routers.${CONTAINER_NAME:-giphytenormatrixproxy}.tls: true
      traefik.http.routers.${CONTAINER_NAME:-giphytenormatrixproxy}.tls.certresolver: ${CERT_RESOLVER:-le}
      traefik.http.routers.${CONTAINER_NAME:-giphytenormatrixproxy}.service: ${CONTAINER_NAME:-giphytenormatrixproxy}-service
      traefik.http.services.${CONTAINER_NAME:-giphytenormatrixproxy}-service.loadbalancer.server.port: ${PROXY_PORT:-8008}

networks:
  matrix:
    name: ${NETWORK_NAME:-matrix}
```

## Usage

1. Start the service:
```bash
docker-compose up -d
```

2. Configure your Matrix client to use the proxy:
- Set up `.well-known` delegation for your domain, or
- Directly proxy your domain to this service

3. Access the web interface at `https://giphy.example.com`

## Matrix Client Integration

To integrate the GIF picker into your Element Matrix client:

1. In Element, use the `/devtools` command
2. Select "Explore Account Data"
3. Set the following data for the key `m.widgets`:
```json
{
  "stickerpicker": {
    "content": {
      "type": "m.stickerpicker",
      "url": "https://your.domain.here/?theme=$theme",
      "name": "Stickerpicker",
      "creatorUserId": "@your-user-id:your.domain.here",
      "data": {}
    },
    "sender": "@your-user-id:your.domain.here",
    "state_key": "stickerpicker",
    "type": "m.widget",
    "id": "stickerpicker"
  }
}
```
4. Replace `your.domain.here` with your proxy's domain and `your-user-id` with your Matrix ID
5. After saving, you should now see a GIF button in your message composer

Note: You may need to refresh Element or reload the page for changes to take effect.

## Configuration Options

See `example-config.yaml` for all available configuration options and their descriptions.

## License

This project is licensed under the GNU Affero General Public License v3.0 (AGPL-3.0). This is a derivative work based on [maunium/stickerpicker](https://github.com/maunium/stickerpicker/), which is also licensed under AGPL-3.0.

For more information, see:
- [LICENSE](LICENSE) file in this repository
- [maunium/stickerpicker](https://github.com/maunium/stickerpicker/) original project
- [GNU AGPL-3.0 License](https://www.gnu.org/licenses/agpl-3.0.en.html)

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request. Make sure to follow the AGPL-3.0 license requirements when contributing.
