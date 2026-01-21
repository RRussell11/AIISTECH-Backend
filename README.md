# AIISTECH-Backend

AI-powered backend for web design and automated systems. This backend provides REST APIs for generating web designs using AI, automating common development tasks, and managing design templates.

## Features

- **AI Web Design Generation**: Generate complete web pages with HTML and CSS using AI
- **Automated Systems**: Automate component generation, code optimization, and workflow creation
- **Template Management**: Pre-built templates for landing pages, dashboards, and portfolios
- **RESTful API**: Easy-to-use REST endpoints for all features
- **Multiple Design Styles**: Support for modern, minimal, corporate, creative, and elegant designs
- **Color Schemes**: Light, dark, colorful, and monochrome themes

## Technology Stack

- **Framework**: FastAPI
- **AI Integration**: OpenAI GPT (with fallback templates)
- **Python**: 3.11+
- **Deployment**: Docker & Docker Compose

## Installation

### Using Docker (Recommended)

1. Clone the repository:
```bash
git clone https://github.com/RRussell11/AIISTECH-Backend.git
cd AIISTECH-Backend
```

2. Create `.env` file from example:
```bash
cp .env.example .env
# Edit .env and add your API keys
```

3. Run with Docker Compose:
```bash
docker-compose up
```

### Manual Installation

1. Install dependencies:
```bash
pip install -r requirements.txt
```

2. Create `.env` file with your configuration:
```bash
cp .env.example .env
```

3. Run the application:
```bash
python main.py
```

Or using uvicorn directly:
```bash
uvicorn main:app --host 0.0.0.0 --port 8000 --reload
```

## API Documentation

Once the server is running, visit:
- **Interactive API Docs**: http://localhost:8000/docs
- **Alternative Docs**: http://localhost:8000/redoc

## API Endpoints

### Design Endpoints

- `POST /api/design/generate` - Generate a web design
- `GET /api/design/styles` - List available design styles
- `GET /api/design/color-schemes` - List color schemes

### Automation Endpoints

- `POST /api/automation/task` - Create automation task
- `GET /api/automation/task/{task_id}` - Get task status
- `GET /api/automation/tasks` - List all tasks
- `GET /api/automation/task-types` - List task types

### Template Endpoints

- `GET /api/templates/` - List all templates
- `GET /api/templates/{template_id}` - Get specific template
- `GET /api/templates/categories/list` - List categories

## Usage Examples

### Generate a Design

```bash
curl -X POST "http://localhost:8000/api/design/generate" \
  -H "Content-Type: application/json" \
  -d '{
    "description": "Modern tech startup landing page",
    "style": "modern",
    "color_scheme": "light",
    "components": ["hero", "features", "contact"]
  }'
```

### Create an Automation Task

```bash
curl -X POST "http://localhost:8000/api/automation/task" \
  -H "Content-Type: application/json" \
  -d '{
    "task_type": "generate_component",
    "parameters": {
      "name": "SubmitButton",
      "type": "button"
    },
    "description": "Generate a custom button component"
  }'
```

### Get Templates

```bash
curl "http://localhost:8000/api/templates/?category=marketing"
```

## Configuration

Environment variables (`.env` file):

```
# AI Service API Keys (optional - uses fallback templates if not provided)
OPENAI_API_KEY=your_openai_api_key_here
ANTHROPIC_API_KEY=your_anthropic_api_key_here

# Server Configuration
HOST=0.0.0.0
PORT=8000
DEBUG=False

# AI Settings
MAX_TOKENS=2000
TEMPERATURE=0.7
```

## Development

### Project Structure

```
AIISTECH-Backend/
├── app/
│   ├── __init__.py
│   ├── config.py              # Configuration settings
│   ├── models/
│   │   ├── __init__.py
│   │   └── schemas.py         # Pydantic models
│   ├── routers/
│   │   ├── __init__.py
│   │   ├── design.py          # Design endpoints
│   │   ├── automation.py      # Automation endpoints
│   │   └── templates.py       # Template endpoints
│   └── services/
│       ├── __init__.py
│       ├── ai_design.py       # AI design generation
│       ├── automation.py      # Automation logic
│       └── templates.py       # Template management
├── main.py                    # Application entry point
├── requirements.txt           # Python dependencies
├── Dockerfile                 # Docker configuration
├── docker-compose.yml         # Docker Compose setup
├── .env.example              # Example environment file
├── .gitignore                # Git ignore rules
└── README.md                 # This file
```

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is open source and available under the MIT License.
