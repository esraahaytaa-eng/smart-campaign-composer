# Smart Campaign Composer

## 1. Project Overview

Smart Campaign Composer is an AI-based marketing tool.

The main goal is to help marketers create SMS and email campaigns using natural language.

The marketer writes a short campaign request. Claude suggests the target audience and creates SMS and email messages.

The marketer can review, edit, and approve the campaign before sending it.

**AI suggests. The backend checks. The marketer approves. The system sends.**

## 2. User Flow

Example request:

"Send a 30% discount reminder to users in Istanbul who added products to their cart in the last 30 days but did not buy anything. Keep the message short and friendly."

The application should follow these steps:

1. **Create Audience:** Claude creates audience rules from the request. The backend checks the rules and shows how many users match.
2. **Generate Content:** Claude creates SMS and email content. The content appears on the screen using streaming.
3. **Review and Approve:** The marketer checks the audience and messages, makes changes if needed, and approves the campaign.
4. **Send Messages:** The system sends approved messages through a queue to mock SMS and email providers.
5. **Track Results:** The dashboard shows live sending status.

No real SMS or email messages will be sent. All users and providers are fake.

## 3. Functional Requirements

### Audience

- Claude creates audience rules from the campaign request.
- The backend must validate these rules.
- Never run SQL generated directly by Claude.
- Show the number of matching users.
- Check SMS and email permissions separately.
- Never send a message if the user has not allowed that channel.

### Content

- Generate SMS and email messages with Claude.
- Stream the generated content to the frontend.
- SMS messages must have a maximum of 160 characters.
- Email subjects must have a maximum of 80 characters.
- Support variables like `{{first_name}}`.
- Allow the marketer to preview and edit messages before approval.

### Sending

- Use an asynchronous queue.
- Send a maximum of 100 messages per second for each channel.
- Do not send duplicate messages to the same user.
- Implement a retry strategy for failed messages.
- Use mock SMS and email providers.

### Monitoring

- Show live delivery status on the dashboard.
- Provide metrics endpoints.
- Provide health check endpoints.

## 4. AI Requirements

Claude is an important part of this project.

- Use Claude to understand campaign requests.
- Use Claude to generate audience rules and messages.
- AI output must use structured JSON.
- The backend must validate the AI output.
- Handle incorrect or invalid AI output safely.
- Tests must work without a real API key by using a mock LLM.

The application must read the Claude API key from the `ANTHROPIC_API_KEY` environment variable in `.env`.

Add a `.env.example` file to the repository.

## 5. Test Data

Create fictional user data for testing.

For this project, the goal is to create **3,000 fictional users**.

The data should include useful information such as:

- First name and last name
- Email and phone number
- City
- Shopping and cart history
- SMS and email permissions

The data must support different audience rules and campaign examples.

## 6. Mock Providers

Build mock SMS and email providers.

These providers must simulate real sending behavior.

They should support:

- Successful messages
- Delays
- Temporary errors
- HTTP 429 responses

This will help test retries, rate limits, and failed messages.

## 7. Technology Requirements

The case requires:

- **Frontend:** Vue.js
- **Backend:** Go

The database, queue, frameworks, and other libraries will be selected after technical analysis.

The goal is to build a simple, reliable, and easy-to-maintain application.

## 8. Required Deliverables

The final project must include:

- A GitHub or GitLab repository.
- One-command setup with `docker compose up`.
- Database setup and fictional user data included in the setup.
- Mock providers included in Docker Compose.
- Working tests with `make test`.
- `README.md` with setup instructions and a short architecture overview.
- `DECISIONS.md` with important technical decisions and their reasons.
- `ai-sessions/` with exports of all Claude Code development sessions.

For our project, a fresh `docker compose up` must automatically create a few thousand fictional users.

Running the application again must not create duplicate users.

Before delivery, all AI session exports must be checked for API keys and personal information.

## 9. Optional Features

The case also includes optional features:

- Scheduled sending
- A/B testing
- Messages based on the user's language
- CI pipeline

The main workflow is the first priority.

Optional features can be added after the core system works correctly.

If something is not completed, explain it clearly in the README.

## 10. Development Process

Claude Code will be used during the full development process.

This includes:

- Requirement analysis
- Architecture and technical design
- Task planning
- Implementation
- Testing and debugging
- Code review
- Documentation
- Deployment preparation

Important technical decisions will be reviewed before implementation.

The work should be divided into small tasks. Each task should have clear goals and tests.

Keep the solution simple, but do not skip important security or reliability requirements.