package inspiration

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"wledger/internal/core"
	"wledger/internal/models"
)

// Store defines what this module needs from the database
// Currently, it just needs to read the parts catalog
type Store interface {
	GetParts() ([]models.Part, error)
}

type Handler struct {
	store     Store
	templates core.TemplateExecutor
}

func New(s Store, t core.TemplateExecutor) *Handler {
	return &Handler{store: s, templates: t}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/inspiration", h.handleShowInspiration)
}

// Handlers

func (h *Handler) handleShowInspiration(w http.ResponseWriter, r *http.Request) {
	// Get all parts
	parts, err := h.store.GetParts()
	if err != nil {
		core.ServerError(w, r, err)
		return
	}

	// Build the inventory list string
	var inventoryList strings.Builder
	for _, part := range parts {
		if part.TotalQuantity > 0 {
			inventoryList.WriteString(fmt.Sprintf("- %s", part.Name))
			if part.PartNumber.Valid {
				inventoryList.WriteString(fmt.Sprintf(" (%s)", part.PartNumber.String))
			}
			inventoryList.WriteString(fmt.Sprintf(": %d in stock\n", part.TotalQuantity))
		}
	}

	// Define the base prompt
	basePrompt := ` ### Project Idea Generator Prompt
Generate 3 - 5 project ideas based on the provided [INVENTORY LIST] following all instructions.

### Objective
The goal of this prompt is to generate innovative and practical project ideas, focusing primarily on maximizing the use of a user-provided list of existing components. The generated projects must be feasible with the materials on hand and should inspire the user.

### Role
You are an expert project ideator and hardware hacker. Your task is to analyze a provided inventory list and generate a list of creative, functional, and engaging project ideas that utilize the components listed.

### Context
The user will provide an inventory list detailing the components they currently have in stock. This list will include the component's name and the quantity available. The user is looking for inspiration for new projects and wants to ensure the ideas are highly relevant to their existing materials.

### Instructions

1.  **Analyze the Inventory:** Thoroughly review the provided \[INVENTORY LIST\] for key components, quantities, and potential synergistic pairings (e.g., microcontrollers, sensors, actuators, power sources).
2.  **Generate Ideas:** Create a list of 3-5 distinct project ideas.
    *   **Priority:** Focus on projects that utilize the most valuable or most numerous components in the inventory.
    *   **Feasibility:** Ensure the projects are realistically achievable *primarily* with the listed parts. If a common, easily obtainable component (e.g., standard wire, basic resistor) is needed, it can be mentioned, but the core functionality must rely on the provided inventory.
3.  **Structure the Output:** For each project idea, provide the following mandatory sections:
    *   **Project Title:** A concise and compelling name.
    *   **Concept Summary:** A brief, engaging description of what the project does and its utility or entertainment value.
    *   **Key Components Used:** A list of essential components from the user's \[INVENTORY LIST\] required for this project. State the required quantity for each.
    *   **Next Steps/Inspiration:** A suggestion for how the user could expand or modify the project if they were to acquire two to three additional, specific components (e.g., "Add a Wi-Fi module for remote control").
4.  **Tone:** Enthusiastic, practical, and inspiring.

### Constraint
Do not suggest projects that require highly specialized or expensive components *not* present in the \[INVENTORY LIST\]. The ideas must be centered around the given materials.

***

### User Input:
#### INVENTORY LIST
`

	finalPrompt := basePrompt + inventoryList.String()

	data := map[string]interface{}{
		"Title":  "Project Inspiration",
		"Prompt": finalPrompt,
	}

	err = h.templates.ExecuteTemplate(w, "inspiration.html", data)
	if err != nil {
		core.ServerError(w, r, err)
	}
}
