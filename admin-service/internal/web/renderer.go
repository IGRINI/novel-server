package web

import (
	"fmt"
	"html/template"
	"io"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
)

// TemplateRenderer реализует интерфейс echo.Renderer для html/template.
type TemplateRenderer struct {
	Templates   *template.Template
	logger      *zap.Logger
	debug       bool   // Если true, шаблоны будут перезагружаться при каждом рендере
	templateDir string // Путь к директории с шаблонами
	funcMap     template.FuncMap
}

// NewTemplateRenderer создает и инициализирует рендерер.
func NewTemplateRenderer(templateDir string, debug bool, logger *zap.Logger, funcMap template.FuncMap) *TemplateRenderer {
	log := logger.Named("TemplateRenderer")
	r := &TemplateRenderer{
		logger:      log,
		debug:       debug,
		templateDir: templateDir,
		funcMap:     funcMap,
	}
	r.loadTemplates() // Загружаем шаблоны при инициализации
	return r
}

// loadTemplates загружает все *.html шаблоны из директории.
func (t *TemplateRenderer) loadTemplates() {
	var err error

	baseTemplate := template.New("").Funcs(t.funcMap)

	// Parse layout first
	layoutPath := filepath.Join(t.templateDir, "layout.html")
	t.Templates, err = baseTemplate.ParseFiles(layoutPath)
	if err != nil {
		t.logger.Error("Failed to parse layout template", zap.String("path", layoutPath), zap.Error(err))
		t.Templates = nil // Ensure it's nil if layout fails
		if !t.debug {     // In non-debug, this might be fatal depending on requirements
			t.logger.Fatal("Layout template failed to parse in non-debug mode", zap.Error(err))
		}
		return // Return here, Render will handle nil t.Templates in debug
	}
	// Then parse the rest, associating them with the layout template set
	contentFilesPattern := filepath.Join(t.templateDir, "*.html") // Re-include layout, ParseGlob handles duplicates
	t.Templates, err = t.Templates.ParseGlob(contentFilesPattern)
	if err != nil {
		t.logger.Error("Failed to parse content templates", zap.String("pattern", contentFilesPattern), zap.Error(err))
		t.Templates = nil // Parsing failed
		if !t.debug {
			t.logger.Fatal("Content templates failed to parse in non-debug mode", zap.Error(err))
		}
	}
	t.logger.Info("Templates loaded (non-debug)", zap.String("dir", t.templateDir))
}

// Render реализует метод интерфейса echo.Renderer.
func (t *TemplateRenderer) Render(w io.Writer, name string, data interface{}, c echo.Context) error {

	// В режиме debug парсим layout + requested template заново при каждом рендере
	if t.debug {
		layoutPath := filepath.Join(t.templateDir, "layout.html")
		templatePath := filepath.Join(t.templateDir, name) // name is like "login.html"

		t.logger.Debug("Parsing layout and specific template from scratch (debug mode)",
			zap.String("layout", layoutPath),
			zap.String("template", templatePath),
		)

		tmpl, err := template.New("").Funcs(t.funcMap).ParseFiles(layoutPath, templatePath)
		if err != nil {
			t.logger.Error("Failed to parse templates on demand (layout+specific)",
				zap.String("layout", layoutPath),
				zap.String("template", templatePath),
				zap.Error(err),
			)
			return fmt.Errorf("failed to parse templates: %w", err)
		}

		err = tmpl.ExecuteTemplate(w, "layout.html", data)
		if err != nil {
			t.logger.Error("Failed to execute layout template with specific data", zap.String("layout", "layout.html"), zap.String("specificTemplate", name), zap.Error(err))
			return fmt.Errorf("template execution failed for %s: %w", name, err)
		}
		return nil

	} else {
		// В обычном режиме используем предзагруженные
		if t.Templates == nil {
			// Attempt to load if not preloaded (e.g., if initial load failed but we want to retry)
			t.logger.Warn("Templates were not preloaded or failed initially, attempting to load now")
			t.loadTemplates()
			if t.Templates == nil {
				t.logger.Error("Failed to load templates even on attempt within Render (non-debug)")
				return fmt.Errorf("templates could not be loaded")
			}
		}

		// Use the preloaded set
		tmpl := t.Templates.Lookup(name)
		if tmpl == nil {
			t.logger.Error("Template not found in preloaded set", zap.String("templateName", name))
			return fmt.Errorf("template %s not found", name)
		}
		t.logger.Debug("Executing preloaded template", zap.String("templateName", name))
		err := tmpl.Execute(w, data)
		if err != nil {
			t.logger.Error("Failed to execute preloaded template", zap.String("templateName", name), zap.Error(err))
		}
		return err
	}
}
