"""
AI service for generating web designs
"""
import os
from typing import Optional
from openai import AsyncOpenAI
from app.config import settings
from app.models.schemas import DesignRequest, DesignStyle, ColorScheme


class AIDesignService:
    """Service for AI-powered web design generation"""
    
    def __init__(self):
        self.client = None
        if settings.OPENAI_API_KEY:
            self.client = AsyncOpenAI(api_key=settings.OPENAI_API_KEY)
    
    async def generate_design(self, request: DesignRequest) -> tuple[str, str]:
        """
        Generate HTML and CSS based on design request
        
        Args:
            request: Design request parameters
            
        Returns:
            Tuple of (html, css)
        """
        if not self.client:
            # Fallback to template-based design if no API key
            return self._generate_fallback_design(request)
        
        try:
            prompt = self._build_design_prompt(request)
            
            response = await self.client.chat.completions.create(
                model="gpt-3.5-turbo",
                messages=[
                    {
                        "role": "system",
                        "content": "You are an expert web designer. Generate clean, modern, and responsive HTML and CSS code."
                    },
                    {
                        "role": "user",
                        "content": prompt
                    }
                ],
                max_tokens=settings.MAX_TOKENS,
                temperature=settings.TEMPERATURE
            )
            
            content = response.choices[0].message.content
            html, css = self._parse_design_response(content)
            
            return html, css
            
        except Exception as e:
            print(f"Error generating design with AI: {e}")
            return self._generate_fallback_design(request)
    
    def _build_design_prompt(self, request: DesignRequest) -> str:
        """Build the prompt for AI design generation"""
        components_str = ", ".join(request.components) if request.components else "standard web components"
        
        prompt = f"""
Generate a complete web page design with the following specifications:

Description: {request.description}
Style: {request.style.value}
Color Scheme: {request.color_scheme.value}
Components: {components_str}
{f'Additional Requirements: {request.additional_requirements}' if request.additional_requirements else ''}

Please provide:
1. Complete HTML structure with semantic markup
2. Modern CSS with responsive design
3. Clean, professional appearance

Format your response as:
HTML:
```html
[HTML code here]
```

CSS:
```css
[CSS code here]
```
"""
        return prompt
    
    def _parse_design_response(self, content: str) -> tuple[str, str]:
        """Parse HTML and CSS from AI response"""
        html = ""
        css = ""
        
        # Extract HTML
        if "```html" in content:
            html_start = content.find("```html") + 7
            html_end = content.find("```", html_start)
            html = content[html_start:html_end].strip()
        
        # Extract CSS
        if "```css" in content:
            css_start = content.find("```css") + 6
            css_end = content.find("```", css_start)
            css = content[css_start:css_end].strip()
        
        return html, css
    
    def _generate_fallback_design(self, request: DesignRequest) -> tuple[str, str]:
        """Generate a basic design without AI"""
        style_class = request.style.value
        color_class = request.color_scheme.value
        
        html = f"""<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{request.description}</title>
    <link rel="stylesheet" href="styles.css">
</head>
<body class="{style_class} {color_class}">
    <header>
        <nav>
            <div class="logo">Your Logo</div>
            <ul class="nav-links">
                <li><a href="#home">Home</a></li>
                <li><a href="#about">About</a></li>
                <li><a href="#services">Services</a></li>
                <li><a href="#contact">Contact</a></li>
            </ul>
        </nav>
    </header>
    
    <main>
        <section class="hero">
            <h1>{request.description}</h1>
            <p>Welcome to your AI-generated web design</p>
            <button class="cta-button">Get Started</button>
        </section>
        
        <section class="features">
            <h2>Features</h2>
            <div class="feature-grid">
                <div class="feature-card">
                    <h3>Feature 1</h3>
                    <p>Description of feature 1</p>
                </div>
                <div class="feature-card">
                    <h3>Feature 2</h3>
                    <p>Description of feature 2</p>
                </div>
                <div class="feature-card">
                    <h3>Feature 3</h3>
                    <p>Description of feature 3</p>
                </div>
            </div>
        </section>
    </main>
    
    <footer>
        <p>&copy; 2026 Your Company. All rights reserved.</p>
    </footer>
</body>
</html>"""
        
        css = self._generate_style_css(request.style, request.color_scheme)
        
        return html, css
    
    def _generate_style_css(self, style: DesignStyle, color_scheme: ColorScheme) -> str:
        """Generate CSS based on style and color scheme"""
        
        # Color palettes
        color_palettes = {
            ColorScheme.LIGHT: {
                "bg": "#ffffff",
                "text": "#333333",
                "primary": "#007bff",
                "secondary": "#6c757d",
                "accent": "#17a2b8"
            },
            ColorScheme.DARK: {
                "bg": "#1a1a1a",
                "text": "#f8f9fa",
                "primary": "#0d6efd",
                "secondary": "#6c757d",
                "accent": "#20c997"
            },
            ColorScheme.COLORFUL: {
                "bg": "#f0f8ff",
                "text": "#2c3e50",
                "primary": "#e74c3c",
                "secondary": "#3498db",
                "accent": "#f39c12"
            },
            ColorScheme.MONOCHROME: {
                "bg": "#ffffff",
                "text": "#000000",
                "primary": "#555555",
                "secondary": "#888888",
                "accent": "#333333"
            }
        }
        
        colors = color_palettes.get(color_scheme, color_palettes[ColorScheme.LIGHT])
        
        css = f"""* {{
    margin: 0;
    padding: 0;
    box-sizing: border-box;
}}

body {{
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, Cantarell, sans-serif;
    line-height: 1.6;
    color: {colors['text']};
    background-color: {colors['bg']};
}}

header {{
    background-color: {colors['primary']};
    color: white;
    padding: 1rem 2rem;
    box-shadow: 0 2px 4px rgba(0,0,0,0.1);
}}

nav {{
    display: flex;
    justify-content: space-between;
    align-items: center;
    max-width: 1200px;
    margin: 0 auto;
}}

.logo {{
    font-size: 1.5rem;
    font-weight: bold;
}}

.nav-links {{
    display: flex;
    list-style: none;
    gap: 2rem;
}}

.nav-links a {{
    color: white;
    text-decoration: none;
    transition: opacity 0.3s;
}}

.nav-links a:hover {{
    opacity: 0.8;
}}

main {{
    max-width: 1200px;
    margin: 0 auto;
    padding: 2rem;
}}

.hero {{
    text-align: center;
    padding: 4rem 2rem;
    background: linear-gradient(135deg, {colors['primary']}, {colors['accent']});
    color: white;
    border-radius: 8px;
    margin-bottom: 3rem;
}}

.hero h1 {{
    font-size: 3rem;
    margin-bottom: 1rem;
}}

.hero p {{
    font-size: 1.25rem;
    margin-bottom: 2rem;
}}

.cta-button {{
    background-color: white;
    color: {colors['primary']};
    border: none;
    padding: 1rem 2rem;
    font-size: 1rem;
    border-radius: 4px;
    cursor: pointer;
    transition: transform 0.3s;
}}

.cta-button:hover {{
    transform: translateY(-2px);
}}

.features {{
    margin-bottom: 3rem;
}}

.features h2 {{
    text-align: center;
    font-size: 2rem;
    margin-bottom: 2rem;
    color: {colors['primary']};
}}

.feature-grid {{
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(250px, 1fr));
    gap: 2rem;
}}

.feature-card {{
    padding: 2rem;
    background: {colors['bg']};
    border: 2px solid {colors['secondary']};
    border-radius: 8px;
    transition: transform 0.3s;
}}

.feature-card:hover {{
    transform: translateY(-4px);
    box-shadow: 0 4px 8px rgba(0,0,0,0.1);
}}

.feature-card h3 {{
    color: {colors['primary']};
    margin-bottom: 1rem;
}}

footer {{
    background-color: {colors['primary']};
    color: white;
    text-align: center;
    padding: 2rem;
    margin-top: 4rem;
}}

@media (max-width: 768px) {{
    .hero h1 {{
        font-size: 2rem;
    }}
    
    .nav-links {{
        flex-direction: column;
        gap: 1rem;
    }}
}}"""
        
        return css


# Singleton instance
ai_design_service = AIDesignService()
