"""
Template service for managing design templates
"""
import uuid
from typing import List, Optional, Dict, Any


class TemplateService:
    """Service for managing design templates"""
    
    def __init__(self):
        self.templates: Dict[str, Dict[str, Any]] = {}
        self._load_default_templates()
    
    def _load_default_templates(self):
        """Load default templates"""
        default_templates = [
            {
                "name": "Landing Page",
                "description": "Modern landing page template",
                "category": "marketing",
                "html": self._get_landing_page_html(),
                "css": self._get_landing_page_css()
            },
            {
                "name": "Dashboard",
                "description": "Admin dashboard template",
                "category": "admin",
                "html": self._get_dashboard_html(),
                "css": self._get_dashboard_css()
            },
            {
                "name": "Portfolio",
                "description": "Portfolio showcase template",
                "category": "portfolio",
                "html": self._get_portfolio_html(),
                "css": self._get_portfolio_css()
            }
        ]
        
        for template in default_templates:
            template_id = str(uuid.uuid4())
            self.templates[template_id] = {
                "template_id": template_id,
                **template,
                "preview_url": None
            }
    
    def get_template(self, template_id: str) -> Optional[Dict[str, Any]]:
        """Get template by ID"""
        return self.templates.get(template_id)
    
    def list_templates(self, category: Optional[str] = None) -> List[Dict[str, Any]]:
        """List all templates, optionally filtered by category"""
        templates = list(self.templates.values())
        if category:
            templates = [t for t in templates if t.get("category") == category]
        return templates
    
    def create_template(self, name: str, description: str, category: str, html: str, css: str) -> Dict[str, Any]:
        """Create a new template"""
        template_id = str(uuid.uuid4())
        template = {
            "template_id": template_id,
            "name": name,
            "description": description,
            "category": category,
            "html": html,
            "css": css,
            "preview_url": None
        }
        self.templates[template_id] = template
        return template
    
    def _get_landing_page_html(self) -> str:
        return """<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Landing Page</title>
</head>
<body>
    <header>
        <nav>
            <div class="logo">Brand</div>
            <ul>
                <li><a href="#features">Features</a></li>
                <li><a href="#pricing">Pricing</a></li>
                <li><a href="#contact">Contact</a></li>
            </ul>
        </nav>
    </header>
    <section class="hero">
        <h1>Welcome to Our Platform</h1>
        <p>Transform your business with our innovative solutions</p>
        <button class="cta">Get Started</button>
    </section>
</body>
</html>"""
    
    def _get_landing_page_css(self) -> str:
        return """body { font-family: Arial, sans-serif; margin: 0; padding: 0; }
header { background: #007bff; color: white; padding: 1rem; }
nav { display: flex; justify-content: space-between; align-items: center; }
.hero { text-align: center; padding: 4rem 2rem; }
.cta { background: #007bff; color: white; border: none; padding: 1rem 2rem; font-size: 1rem; cursor: pointer; }"""
    
    def _get_dashboard_html(self) -> str:
        return """<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Dashboard</title>
</head>
<body>
    <div class="dashboard">
        <aside class="sidebar">
            <h2>Menu</h2>
            <ul>
                <li><a href="#overview">Overview</a></li>
                <li><a href="#analytics">Analytics</a></li>
                <li><a href="#settings">Settings</a></li>
            </ul>
        </aside>
        <main class="content">
            <h1>Dashboard</h1>
            <div class="stats">
                <div class="stat-card">
                    <h3>Users</h3>
                    <p>1,234</p>
                </div>
            </div>
        </main>
    </div>
</body>
</html>"""
    
    def _get_dashboard_css(self) -> str:
        return """body { margin: 0; font-family: Arial, sans-serif; }
.dashboard { display: flex; height: 100vh; }
.sidebar { width: 250px; background: #2c3e50; color: white; padding: 2rem; }
.content { flex: 1; padding: 2rem; background: #ecf0f1; }
.stats { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 1rem; }
.stat-card { background: white; padding: 1.5rem; border-radius: 8px; }"""
    
    def _get_portfolio_html(self) -> str:
        return """<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Portfolio</title>
</head>
<body>
    <header>
        <h1>My Portfolio</h1>
        <p>Creative Designer & Developer</p>
    </header>
    <section class="projects">
        <div class="project">
            <h3>Project 1</h3>
            <p>Description of project 1</p>
        </div>
        <div class="project">
            <h3>Project 2</h3>
            <p>Description of project 2</p>
        </div>
    </section>
</body>
</html>"""
    
    def _get_portfolio_css(self) -> str:
        return """body { font-family: 'Georgia', serif; margin: 0; padding: 0; }
header { text-align: center; padding: 4rem 2rem; background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); color: white; }
.projects { display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: 2rem; padding: 2rem; }
.project { background: white; padding: 2rem; border-radius: 8px; box-shadow: 0 2px 8px rgba(0,0,0,0.1); }"""


# Singleton instance
template_service = TemplateService()
