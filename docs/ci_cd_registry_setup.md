# CI/CD Container Registry Setup

This guide documents the values required by the CI/CD workflows to push and pull
container images. Populate these secrets in the Gitea repository **before**
triggering the pipelines so that the deploy webhook receives runnable images.

## Required Secrets
The workflows expect the following secrets:

| Secret | Source | Usage |
| --- | --- | --- |
| `REGISTRY_URL` | Fully qualified address of your container registry (e.g. `git.funwan.live`). | Used by CI and CD to build the canonical image reference. |
| `REGISTRY_REPOSITORY` | Repository path inside the registry (e.g. `stream/funpot-core`). | Combined with the URL and Git SHA to tag the image. |
| `REGISTRY_USERNAME` | Service account or robot user with push/pull permissions. | Authenticates Docker login in CI/CD workflows. |
| `REGISTRY_PASSWORD` | Token or password generated for the above user. | Authenticates Docker login in CI/CD workflows. |
| `FUNPOT_AUTH_TELEGRAM_BOT_TOKEN` | Token issued by BotFather for the Telegram Mini App bot. | Enables smoke tests to authenticate against the service. |
| `DEV_DEPLOY_WEBHOOK_URL` | HTTPS endpoint of your deployment handler for the `dev` environment. | Receives image metadata and boots the container. |
| `PROD_DEPLOY_WEBHOOK_URL` | HTTPS endpoint of your deployment handler for the `main` environment. | Receives image metadata and boots the container. |
| `DEV_DEPLOY_HEALTHCHECK_URL` | Base URL of the running dev environment (e.g. `https://dev.funpot.live/readyz`). | Polled by the CD workflow to confirm the application is up after the webhook finishes. |
| `PROD_DEPLOY_HEALTHCHECK_URL` | Base URL of the running production environment (e.g. `https://funpot.live/readyz`). | Optional health probe for the production rollout; leave blank to skip verification. |

## Where to Obtain the Values
1. **Registry URL & Repository** – defined when you create a project in your
   registry (Docker Hub, Harbor, GitLab, or a self-hosted `registry:2`). Copy the
   hostname and repository namespace exactly as shown in the registry UI.
2. **Credentials** – create a robot account or personal access token in the
   registry with `push` and `pull` permissions. Use its username and generated
   token as the `REGISTRY_USERNAME`/`REGISTRY_PASSWORD` secrets.
3. **Telegram Bot Token** – talk to [@BotFather](https://t.me/BotFather) in
   Telegram, create the bot, and store the provided token as a secret.
4. **Deployment Webhooks** – deploy an HTTP service in your infrastructure that
   can accept the CD webhook payload (`environment`, `branch`, `image`, `sha`).
   The service should perform `docker pull`, stop the old container, run the new
   one (e.g. `docker run -d --name funpot-core -p 8080:8080 --env-file /etc/funpot.env <image>`),
   and wait for `/healthz` and `/readyz` to return `200` before responding. Once
   the service is reachable, expose its health endpoint via the corresponding
   `*_DEPLOY_HEALTHCHECK_URL` secret so that the CD workflow can verify the
   deployment automatically.

## Validating the Configuration
- Trigger the CI workflow from a feature branch push. The `build` job should log
  `Login Succeeded` and push the tagged image to the configured registry.
- Verify the image exists in the registry with the expected `<sha>` tag.
- Confirm that the CD workflow completes the `docker pull` step, receives a
  `2xx` response from your deployment webhook, and succeeds on the
  post-deploy healthcheck poll.

If any secret is missing or incorrect, the workflows will fail during Docker
login or `docker pull`, preventing container deployment.
