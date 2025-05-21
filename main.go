package main

import (
	"bytes"
	"context" // Import context package
	"crypto/rand"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors" // Import the errors package
	"fmt"
	"io" // Needed for file uploads
	"log"
	"math" // Import math package for rounding
	"net/http"
	"net/smtp"
	"net/url"
	"os"            // Used for reading environment variable
	"path/filepath" // Needed for file uploads
	"strconv"       // Used for parsing JWTExpiryHours & client ID
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	// Import JWT library (run: go get github.com/golang-jwt/jwt/v5)
	"github.com/golang-jwt/jwt/v5"
	// Import chi router and middleware (run: go get github.com/go-chi/chi/v5)
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors" // Optional: For easier CORS config with chi

	// Import CGO-Free SQLite driver (run: go get modernc.org/sqlite)
	_ "github.com/go-sql-driver/mysql"
)

// --- Configuration ---

type Config struct {
	ListenAddr      string
	DBDSN           string // For MySQL DSN (e.g., "user:password@tcp(127.0.0.1:3306)/dbname?parseTime=true")
	VerificationURL string
	ResetURL        string
	CorsOrigin      string
	MockEmailFrom   string
	JWTSecret       string
	JWTExpiryHours  int
	UploadPath      string
	FrontendURL     string
}

type AgentInsurerRelation struct {
	ID                          int64           `json:"id,omitempty"`
	AgentUserID                 int64           `json:"-"`
	InsurerName                 string          `json:"insurerName"`
	AgentCode                   sql.NullString  `json:"agentCode"`
	SpocEmail                   sql.NullString  `json:"spocEmail"`
	UpfrontCommissionPercentage sql.NullFloat64 `json:"upfrontCommissionPercentage"` // NEW
	TrailCommissionPercentage   sql.NullFloat64 `json:"trailCommissionPercentage"`   // NEW
	ProductID                   string          `json:"product_id"`
	Name                        string          `json:"name"`
	Category                    string          `json:"category"`
	Description                 sql.NullString  `json:"description"`
	Status                      string          `json:"status"`
	Features                    sql.NullString  `json:"features"`
	Eligibility                 sql.NullString  `json:"eligibility"`
	Term                        sql.NullString  `json:"term"`
	Exclusions                  sql.NullString  `json:"exclusions"`
	RoomRent                    sql.NullString  `json:"roomRent"`
	PremiumIndication           sql.NullString  `json:"premiumIndication"`
	InsurerLogoURL              sql.NullString  `json:"insurerLogo"`
	BrochureURL                 sql.NullString  `json:"brochureUrl"`
	WordingURL                  sql.NullString  `json:"wordingUrl"`
	ClaimFormURL                sql.NullString  `json:"claimFormUrl"`
	CreatedAt                   time.Time       `json:"createdAt"`
	UpdatedAt                   sql.NullTime    `json:"updatedAt"`
}

type FullAgentProfileWithRelations struct {
	User                                    // Embed basic user info
	AgentProfile                            // Embed extended profile info
	InsurerRelations []AgentInsurerRelation `json:"insurerRelations"` // Contains new fields
}
type OnboardingPayload struct {
	Name          string   `json:"name"`  // Required
	Email         string   `json:"email"` // Optional
	Phone         string   `json:"phone"` // Optional (one of email/phone required)
	Dob           string   `json:"dob"`
	Address       string   `json:"address"`
	Tags          string   `json:"tags"`
	Income        *float64 `json:"income"`
	MaritalStatus string   `json:"maritalStatus"`
	City          string   `json:"city"`
	JobProfile    string   `json:"jobProfile"`
	Dependents    *int64   `json:"dependents"`
	Liability     *float64 `json:"liability"`
	HousingType   string   `json:"housingType"`
	VehicleCount  *int64   `json:"vehicleCount"`
	VehicleType   string   `json:"vehicleType"`
	VehicleCost   *float64 `json:"vehicleCost"`
}

type RenewalPolicyView struct {
	Policy            // Embed original policy fields
	ClientName string `json:"clientName"`
}

// NEW: Model for Paginated Response (can be used for Tasks, Activity)
type PaginatedResponse struct {
	Items       interface{} `json:"items"` // Can hold []Task, []ActivityLog etc.
	TotalItems  int         `json:"totalItems"`
	CurrentPage int         `json:"currentPage"`
	PageSize    int         `json:"pageSize"`
	TotalPages  int         `json:"totalPages"`
}

type SuggestedTask struct {
	Description string `json:"description"`
	DueDate     string `json:"dueDate"` // Expect YYYY-MM-DD or empty
	IsUrgent    bool   `json:"isUrgent"`
	ClientID    *int64 `json:"clientId,omitempty"` // Optional client ID if task is client-specific
}
type DashboardMetrics struct {
	PoliciesSoldThisMonth int     `json:"policiesSoldThisMonth"`
	UpcomingRenewals30d   int     `json:"upcomingRenewals30d"`
	CommissionThisMonth   float64 `json:"commissionThisMonth"`
	NewLeadsThisWeek      int     `json:"newLeadsThisWeek"`
}
type ActivityLog struct {
	ID           int64     `json:"id"`
	AgentUserID  int64     `json:"agentUserId"`
	Timestamp    time.Time `json:"timestamp"`
	ActivityType string    `json:"activityType"` // e.g., "client_added", "policy_issued"
	Description  string    `json:"description"`  // e.g., "Added client 'Rajesh Kumar'", "Issued policy #POL123"
	RelatedID    string    `json:"relatedId"`    // Optional: ID of the related entity (client, policy etc.)
}
type EstimatedCoverage struct {
	Amount float64  `json:"amount"`
	Unit   string   `json:"unit"` // e.g., "Lakhs", "Crores", "IDV"
	Notes  []string `json:"notes"`
}
type CoverageEstimation struct {
	Health EstimatedCoverage `json:"health"`
	Life   EstimatedCoverage `json:"life"`
	Motor  EstimatedCoverage `json:"motor"`
}

var config Config
var db *sql.DB
var jwtSecretKey []byte

type ClientFullData struct {
	Client         Client          `json:"client"`
	Policies       []Policy        `json:"policies"`
	Communications []Communication `json:"communications"`
	Tasks          []Task          `json:"tasks"` // Includes completed tasks for this view
	Documents      []Document      `json:"documents"`
}

// --- Models ---
type User struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	UserType     string    `json:"userType"`
	IsVerified   bool      `json:"isVerified"`
	CreatedAt    time.Time `json:"createdAt"`
}
type Token struct {
	UserID    int64
	TokenHash string
	Purpose   string
	ExpiresAt time.Time
}
type Claims struct {
	UserID   int64  `json:"user_id"`
	UserType string `json:"user_type"`
	jwt.RegisteredClaims
}
type Notice struct {
	ID          int64     `json:"id"`
	Title       string    `json:"title"`
	Content     string    `json:"content"`
	Category    string    `json:"category"`
	PostedBy    string    `json:"postedBy"`
	IsImportant bool      `json:"isImportant"`
	CreatedAt   time.Time `json:"createdAt"`
}
type Client struct {
	ID              int64           `json:"id"`
	AgentUserID     int64           `json:"agentUserId"`
	Name            string          `json:"name"`
	Email           sql.NullString  `json:"email"`
	Phone           sql.NullString  `json:"phone"`
	Dob             sql.NullString  `json:"dob"`
	Address         sql.NullString  `json:"address"`
	Status          string          `json:"status"`
	Tags            sql.NullString  `json:"tags"`
	LastContactedAt sql.NullTime    `json:"lastContactedAt"`
	CreatedAt       time.Time       `json:"createdAt"`
	Income          sql.NullFloat64 `json:"income"`        // Store as number (e.g., annual income)
	MaritalStatus   sql.NullString  `json:"maritalStatus"` // Single, Married, Divorced, Widowed
	City            sql.NullString  `json:"city"`
	JobProfile      sql.NullString  `json:"jobProfile"`   // Salaried, Business Owner, Professional, Other
	Dependents      sql.NullInt64   `json:"dependents"`   // Number of dependents
	Liability       sql.NullFloat64 `json:"liability"`    // Total outstanding loan amount
	HousingType     sql.NullString  `json:"housingType"`  // Rented, Owned
	VehicleCount    sql.NullInt64   `json:"vehicleCount"` // Number of vehicles
	VehicleType     sql.NullString  `json:"vehicleType"`  // e.g., "Car, Bike", "Car", etc.
	VehicleCost     sql.NullFloat64 `json:"vehicleCost"`
}
type AgentProfile struct {
	UserID        int64          `json:"userId"`
	Mobile        sql.NullString `json:"mobile"`
	Gender        sql.NullString `json:"gender"`
	PostalAddress sql.NullString `json:"postalAddress"`
	AgencyName    sql.NullString `json:"agencyName"` // Relevant if userType is agent and belongs to an agency
	PAN           sql.NullString `json:"pan"`
	BankName      sql.NullString `json:"bankName"`
	BankAccountNo sql.NullString `json:"bankAccountNo"`
	BankIFSC      sql.NullString `json:"bankIfsc"`
}
type AgentGoal struct {
	UserID       int64           `json:"userId"`
	TargetIncome sql.NullFloat64 `json:"targetIncome"`
	TargetPeriod sql.NullString  `json:"targetPeriod"` // e.g., "2025-Q2", "2025-Annual"
}

// Combined struct for GET /api/agents/profile response
type FullAgentProfile struct {
	User         // Embed basic user info
	AgentProfile // Embed extended profile info
}

type ClientPayload struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Phone   string `json:"phone"`
	Dob     string `json:"dob"`
	Address string `json:"address"`
	Status  string `json:"status"`
	Tags    string `json:"tags"`
	// NEW FIELDS (use pointers for optional numeric fields from JSON)
	Income        *float64 `json:"income"`
	MaritalStatus string   `json:"maritalStatus"`
	City          string   `json:"city"`
	JobProfile    string   `json:"jobProfile"`
	Dependents    *int64   `json:"dependents"`
	Liability     *float64 `json:"liability"`
	HousingType   string   `json:"housingType"`
	VehicleCount  *int64   `json:"vehicleCount"`
	VehicleType   string   `json:"vehicleType"`
	VehicleCost   *float64 `json:"vehicleCost"`
}
type Product struct {
	ID                          string          `json:"id"`
	Name                        string          `json:"name"`
	Category                    string          `json:"category"`
	Insurer                     string          `json:"insurer"`
	Description                 sql.NullString  `json:"description"`
	Status                      string          `json:"status"`
	Features                    sql.NullString  `json:"features"`
	Eligibility                 sql.NullString  `json:"eligibility"`
	Term                        sql.NullString  `json:"term"`
	Exclusions                  sql.NullString  `json:"exclusions"`
	RoomRent                    sql.NullString  `json:"roomRent"`
	PremiumIndication           sql.NullString  `json:"premiumIndication"`
	InsurerLogoURL              sql.NullString  `json:"insurerLogo"`
	BrochureURL                 sql.NullString  `json:"brochureUrl"`
	WordingURL                  sql.NullString  `json:"wordingUrl"`
	ClaimFormURL                sql.NullString  `json:"claimFormUrl"`
	UpfrontCommissionPercentage sql.NullFloat64 `json:"upfrontCommissionPercentage"`
	TrailCommissionPercentage   sql.NullFloat64 `json:"trailCommissionPercentage"`
	CreatedAt                   time.Time       `json:"createdAt"`
	UpdatedAt                   sql.NullTime    `json:"updatedAt"`
}

type UpdateInsurerRelationsRequest struct {
	Relations []UpdateInsurerPayload `json:"relations"`
}

type UpdateInsurerPayload struct {
	InsurerName                 string  `json:"insurerName"`
	AgentCode                   string  `json:"agentCode"`
	SpocEmail                   string  `json:"spocEmail"`
	UpfrontCommissionPercentage float64 `json:"upfrontCommissionPercentage"`
	TrailCommissionPercentage   float64 `json:"trailCommissionPercentage"`
}

// NEW: Struct for bulk upload result summary
type BulkUploadResult struct {
	SuccessCount int      `json:"successCount"`
	FailureCount int      `json:"failureCount"`
	Errors       []string `json:"errors"` // List of errors like "Row 5: Duplicate email"
}
type Policy struct {
	ID                      string          `json:"id"`
	ClientID                int64           `json:"clientId"`
	AgentUserID             int64           `json:"agentUserId"`
	ProductID               sql.NullString  `json:"productId"`
	PolicyNumber            string          `json:"policyNumber"`
	Insurer                 string          `json:"insurer"`
	Premium                 float64         `json:"premium"`
	SumInsured              float64         `json:"sumInsured"`
	StartDate               sql.NullString  `json:"startDate"`
	EndDate                 sql.NullString  `json:"endDate"`
	Status                  string          `json:"status"`
	PolicyDocURL            sql.NullString  `json:"policyDocUrl"`
	UpfrontCommissionAmount sql.NullFloat64 `json:"upfrontCommissionAmount"`
	CreatedAt               time.Time       `json:"createdAt"`
	UpdatedAt               sql.NullTime    `json:"updatedAt"`
}
type Communication struct {
	ID          int64     `json:"id"`
	ClientID    int64     `json:"clientId"`
	AgentUserID int64     `json:"agentUserId"`
	Type        string    `json:"type"`
	Timestamp   time.Time `json:"timestamp"`
	Summary     string    `json:"summary"`
	CreatedAt   time.Time `json:"createdAt"`
}
type Task struct {
	ID          int64          `json:"id"`
	ClientID    int64          `json:"clientId"`
	AgentUserID int64          `json:"agentUserId"`
	Description string         `json:"description"`
	DueDate     sql.NullString `json:"dueDate"`
	IsUrgent    bool           `json:"isUrgent"`
	IsCompleted bool           `json:"isCompleted"`
	CreatedAt   time.Time      `json:"createdAt"`
	CompletedAt sql.NullTime   `json:"completedAt"`
}
type Document struct {
	ID           int64     `json:"id"`
	ClientID     int64     `json:"clientId"`
	AgentUserID  int64     `json:"agentUserId"`
	Title        string    `json:"title"`
	DocumentType string    `json:"documentType"`
	FileURL      string    `json:"fileUrl"`
	UploadedAt   time.Time `json:"uploadedAt"`
}
type MarketingCampaign struct {
	ID                int64          `json:"id"`
	AgentUserID       int64          `json:"agentUserId"`
	Name              string         `json:"name"`
	Status            string         `json:"status"`
	TargetSegmentName sql.NullString `json:"targetSegmentName"`
	SentAt            sql.NullTime   `json:"sentAt"`
	StatsOpens        sql.NullInt64  `json:"statsOpens"`
	StatsClicks       sql.NullInt64  `json:"statsClicks"`
	StatsLeads        sql.NullInt64  `json:"statsLeads"`
	CreatedAt         time.Time      `json:"createdAt"`
}
type MarketingTemplate struct {
	ID          int64          `json:"id"`
	Name        string         `json:"name"`
	Type        string         `json:"type"`
	Category    string         `json:"category"`
	PreviewText sql.NullString `json:"previewText"`
	Content     string         `json:"-"`
	CreatedAt   time.Time      `json:"createdAt"`
}

func getAgentInsurerRelations(agentUserID int64) ([]AgentInsurerRelation, error) {
	log.Printf("DATABASE: Getting insurer relations for agent %d\n", agentUserID)
	rows, err := db.Query(`SELECT id, agent_user_id, insurer_name, agent_code, spoc_email,
                           upfront_commission_percentage, trail_commission_percentage
                       FROM agent_insurer_relations WHERE agent_user_id = ? ORDER BY insurer_name ASC`, agentUserID) // Select new columns
	if err != nil {
		log.Printf("ERROR: Query agent relations failed: %v", err)
		return nil, err
	}
	defer rows.Close()

	relations := []AgentInsurerRelation{}
	for rows.Next() {
		var rel AgentInsurerRelation
		// Scan new columns
		if err := rows.Scan(&rel.ID, &rel.AgentUserID, &rel.InsurerName, &rel.AgentCode, &rel.SpocEmail,
			&rel.UpfrontCommissionPercentage, &rel.TrailCommissionPercentage); err != nil {
			log.Printf("ERROR: Scan agent relation row failed: %v", err)
			continue
		}
		relations = append(relations, rel)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return relations, nil
}

// Replaces all existing relations for the agent with the provided list
func setAgentInsurerRelations(agentUserID int64, relations []AgentInsurerRelation) error {
	log.Printf("DATABASE: Setting insurer relations for agent %d (count: %d)\n", agentUserID, len(relations))
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete old relations
	_, err = tx.Exec("DELETE FROM agent_insurer_relations WHERE agent_user_id = ?", agentUserID)
	if err != nil {
		return fmt.Errorf("failed to delete existing relations: %w", err)
	}

	// Prepare insert
	stmt, err := tx.Prepare(`
		INSERT INTO agent_insurer_relations (
			agent_user_id, insurer_name, agent_code, spoc_email, 
			upfront_commission_percentage, trail_commission_percentage,
			name, category, description, status, features, eligibility,
			term, exclusions, room_rent, premium_indication,
			insurer_logo_url, brochure_url, wording_url, claim_form_url,
			created_at,product_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare insert relation: %w", err)
	}
	defer stmt.Close()

	insertCount := 0
	maxRelations := 25
	seenInsurers := make(map[string]bool)

	now := time.Now()

	for i, rel := range relations {
		if i >= maxRelations {
			log.Printf("WARN: Max insurer relations (%d) reached for agent %d.", maxRelations, agentUserID)
			break
		}
		if rel.InsurerName == "" {
			continue
		}
		lowerInsurer := strings.ToLower(rel.InsurerName)
		if seenInsurers[lowerInsurer] {
			log.Printf("WARN: Duplicate insurer '%s' in payload for agent %d, skipping.", rel.InsurerName, agentUserID)
			continue
		}

		_, err = stmt.Exec(
			agentUserID,
			rel.InsurerName,
			rel.AgentCode,
			rel.SpocEmail,
			rel.UpfrontCommissionPercentage,
			rel.TrailCommissionPercentage,
			rel.Name,
			rel.Category,
			rel.Description,
			rel.Status,
			rel.Features,
			rel.Eligibility,
			rel.Term,
			rel.Exclusions,
			rel.RoomRent,
			rel.PremiumIndication,
			rel.InsurerLogoURL,
			rel.BrochureURL,
			rel.WordingURL,
			rel.ClaimFormURL,
			now,
			rel.ProductID,
		)
		if err != nil {
			return fmt.Errorf("failed to insert relation for insurer '%s': %w", rel.InsurerName, err)
		}
		seenInsurers[lowerInsurer] = true
		insertCount++
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	log.Printf("DATABASE: Successfully set %d insurer relations for agent %d\n", insertCount, agentUserID)
	return nil
}

// Gets relation for a specific insurer for an agent
func getAgentInsurerRelationByInsurer(agentUserID int64, insurerName string) (*AgentInsurerRelation, error) {
	row := db.QueryRow(`SELECT id, agent_user_id, insurer_name, agent_code, spoc_email, upfront_commission_percentage, trail_commission_percentage
                       FROM agent_insurer_relations
                       WHERE agent_user_id = ? AND LOWER(insurer_name) = LOWER(?)`,
		agentUserID, insurerName)
	detail := &AgentInsurerRelation{}
	err := row.Scan(&detail.ID, &detail.AgentUserID, &detail.InsurerName, &detail.AgentCode, &detail.SpocEmail, &detail.UpfrontCommissionPercentage, &detail.TrailCommissionPercentage)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, sql.ErrNoRows
		}
		return nil, err
	}
	return detail, nil
}

// UPDATED: createPolicy to use agent-insurer commission first, then product commission
func createPolicy(policy Policy) (string, error) {
	if policy.ID == "" {
		policy.ID = "POL-" + generateSimpleID(8)
	}
	policy.CreatedAt = time.Now()

	// --- Calculate Upfront Commission ---
	var commissionPercentage sql.NullFloat64 // Use NullFloat64
	commissionSource := "None"

	// 1. Try getting agent-specific rate for this insurer
	relation, err := getAgentInsurerRelationByInsurer(policy.AgentUserID, policy.Insurer)
	if err == nil && relation != nil && relation.UpfrontCommissionPercentage.Valid {
		commissionPercentage = relation.UpfrontCommissionPercentage
		commissionSource = "Agent-Insurer Rate"
	} else if err != nil && err != sql.ErrNoRows {
		log.Printf("WARN: Error fetching agent-insurer relation for commission calc (Policy: %s): %v", policy.PolicyNumber, err)
	}

	// 2. If no agent rate, try getting product rate
	if !commissionPercentage.Valid && policy.ProductID.Valid {
		product, err := getProductByID(policy.ProductID.String)
		if err == nil && product != nil && product.UpfrontCommissionPercentage.Valid {
			commissionPercentage = relation.UpfrontCommissionPercentage
			commissionSource = "Product Rate"
		} else if err != nil && err != sql.ErrNoRows {
			log.Printf("WARN: Error fetching product for commission calc (Policy: %s, Product: %s): %v", policy.PolicyNumber, policy.ProductID.String, err)
		}
	}

	// 3. Calculate amount if percentage is valid
	var commissionAmount float64 = 0
	var commissionValid bool = false
	if commissionPercentage.Valid {
		commissionAmount = policy.Premium * (commissionPercentage.Float64 / 100.0)
		commissionAmount = math.Round(commissionAmount*100) / 100 // Round
		commissionValid = true
		log.Printf("DATABASE: Calculated commission for policy %s using %s: %.2f", policy.ID, commissionSource, commissionAmount)
	} else {
		log.Printf("DATABASE: No valid commission percentage found for policy %s (Agent %d, Insurer %s, Product %s)", policy.ID, policy.AgentUserID, policy.Insurer, policy.ProductID.String)
	}
	policy.UpfrontCommissionAmount = sql.NullFloat64{Float64: commissionAmount, Valid: commissionValid}
	// --- End Commission Calculation ---

	stmt, err := db.Prepare(`INSERT INTO policies (id, client_id, agent_user_id, product_id, policy_number, insurer, premium, sum_insured, start_date, end_date, status, policy_doc_url, upfront_commission_amount, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return "", fmt.Errorf("failed to prepare insert policy: %w", err)
	}
	defer stmt.Close()
	_, err = stmt.Exec(policy.ID, policy.ClientID, policy.AgentUserID, policy.ProductID, policy.PolicyNumber, policy.Insurer, policy.Premium, policy.SumInsured, policy.StartDate, policy.EndDate, policy.Status, policy.PolicyDocURL, policy.UpfrontCommissionAmount, policy.CreatedAt)
	if err != nil {
		return "", fmt.Errorf("failed to execute insert policy: %w", err)
	}
	log.Printf("DATABASE: Policy created with ID: %s\n", policy.ID)
	return policy.ID, nil
}

func getClientCountsByStatus(agentUserID int64) (clients []Client, err error) {
	rows, err := db.Query(`SELECT id, name, status, agent_user_id FROM clients WHERE agent_user_id = ?`, agentUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clientList []Client

	for rows.Next() {
		var c Client
		if err := rows.Scan(&c.ID, &c.Name, &c.Status, &c.AgentUserID); err != nil {
			log.Printf("WARN: Error scanning client: %v", err)
			continue
		}
		clientList = append(clientList, c)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return clientList, nil
}

type MarketingContent struct {
	ID           int64          `json:"id"`
	Title        string         `json:"title"`
	ContentType  string         `json:"contentType"`
	Description  sql.NullString `json:"description"`
	GCSURL       string         `json:"gcsUrl"`
	ThumbnailURL sql.NullString `json:"thumbnailUrl"`
	CreatedAt    time.Time      `json:"createdAt"`
}
type ClientSegment struct {
	ID          int64          `json:"id"`
	AgentUserID int64          `json:"agentUserId"`
	Name        string         `json:"name"`
	Criteria    sql.NullString `json:"criteria"`
	ClientCount sql.NullInt64  `json:"clientCount"`
	CreatedAt   time.Time      `json:"createdAt"`
}
type GeminiRequest struct {
	Contents         []GeminiContent         `json:"contents"`
	GenerationConfig *GeminiGenerationConfig `json:"generationConfig,omitempty"`
	// Add SafetySettings if needed
}
type GeminiContent struct {
	Parts []GeminiPart `json:"parts"`
}
type GeminiPart struct {
	Text string `json:"text"`
}
type GeminiResponse struct {
	Candidates     []GeminiCandidate     `json:"candidates"`
	PromptFeedback *GeminiPromptFeedback `json:"promptFeedback,omitempty"`
}
type GeminiCandidate struct {
	Content       GeminiContent        `json:"content"`
	FinishReason  string               `json:"finishReason"`
	Index         int                  `json:"index"`
	SafetyRatings []GeminiSafetyRating `json:"safetyRatings"`
}
type GeminiPromptFeedback struct {
	SafetyRatings []GeminiSafetyRating `json:"safetyRatings"`
}
type GeminiSafetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"`
}
type GeminiGenerationConfig struct {
	Temperature     float32  `json:"temperature,omitempty"`
	TopK            int      `json:"topK,omitempty"`
	TopP            float32  `json:"topP,omitempty"`
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

// NEW: Struct to parse suggested tasks from AI response

// Payloads
type CreateCommunicationPayload struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Summary   string `json:"summary"`
}
type CreateTaskPayload struct {
	Description string `json:"description"`
	DueDate     string `json:"dueDate"`
	IsUrgent    bool   `json:"isUrgent"`
}
type CreatePolicyPayload struct {
	ProductID    string  `json:"productId"`
	PolicyNumber string  `json:"policyNumber"`
	Insurer      string  `json:"insurer"`
	Premium      float64 `json:"premium"`
	SumInsured   float64 `json:"sumInsured"`
	StartDate    string  `json:"startDate"`
	EndDate      string  `json:"endDate"`
	Status       string  `json:"status"`
	PolicyDocURL string  `json:"policyDocUrl"`
}

type AgentInsurerPOC struct {
	// ID is mostly for DB internal use, might not need in JSON response/request often
	ID          int64  `json:"id,omitempty"`
	AgentUserID int64  `json:"-"` // Excluded from JSON, inferred from context
	InsurerName string `json:"insurerName"`
	PocEmail    string `json:"pocEmail"`
}

// Updated struct for GET /api/agents/profile response
type FullAgentProfileWithPOCs struct {
	User                           // Embed basic user info
	AgentProfile                   // Embed extended profile info
	InsurerPOCs  []AgentInsurerPOC `json:"insurerPOCs"` // Add the list of POCs
}

// NEW: Client Portal Token Model
type ClientPortalToken struct {
	Token       string    `json:"token"` // The secure token itself
	ClientID    int64     `json:"clientId"`
	AgentUserID int64     `json:"agentUserId"`
	ExpiresAt   time.Time `json:"expiresAt"`
	CreatedAt   time.Time `json:"createdAt"`
}

type SendProposalPayload struct {
	ClientID  int64  `json:"clientId"`
	ProductID string `json:"productId"`
	// Add other relevant info if needed, like custom message from agent
}

type UpdateInsurerDetailsPayload struct {
	Details []AgentInsurerDetail `json:"details"`
}

// NEW: Struct for data returned to public portal (subset of Client + related)
type PublicClientView struct {
	Client             Client             `json:"client"` // Full client details
	Policies           []Policy           `json:"policies"`
	Documents          []Document         `json:"documents"`
	Communications     []Communication    `json:"communications"`
	CoverageEstimation CoverageEstimation `json:"coverageEstimation"`
	AiRecommendation   string             `json:"aiRecommendation"` // Text from Gemini
}
type UpdateInsurerPOCsPayload struct {
	POCs []AgentInsurerPOC `json:"pocs"`
}

type CreateSegmentPayload struct {
	Name     string `json:"name"`
	Criteria string `json:"criteria"`
}
type UpdateSegmentPayload struct {
	Name     string `json:"name"`
	Criteria string `json:"criteria"`
}
type CreateCampaignPayload struct {
	Name              string `json:"name"`
	TargetSegmentName string `json:"targetSegmentName"`
	TemplateID        *int64 `json:"templateId"`
	Status            string `json:"status"`
}
type CreateProductPayload struct {
	ID                          string   `json:"id"`
	Name                        string   `json:"name"`
	Category                    string   `json:"category"`
	Insurer                     string   `json:"insurer"`
	Description                 *string  `json:"description"`
	Status                      string   `json:"status"`
	Features                    *string  `json:"features"`
	Eligibility                 *string  `json:"eligibility"`
	Term                        *string  `json:"term"`
	Exclusions                  *string  `json:"exclusions"`
	RoomRent                    *string  `json:"roomRent"`
	PremiumIndication           *string  `json:"premiumIndication"`
	InsurerLogoURL              *string  `json:"insurerLogo"`
	BrochureURL                 *string  `json:"brochureUrl"`
	WordingURL                  *string  `json:"wordingUrl"`
	ClaimFormURL                *string  `json:"claimFormUrl"`
	UpfrontCommissionPercentage *float64 `json:"upfrontCommissionPercentage"`
	TrailCommissionPercentage   *float64 `json:"trailCommissionPercentage"`
}
type UpdateAgentProfilePayload struct {
	Mobile        string `json:"mobile"`
	Gender        string `json:"gender"`
	PostalAddress string `json:"postalAddress"`
	AgencyName    string `json:"agencyName"`
	PAN           string `json:"pan"`
	BankName      string `json:"bankName"`
	BankAccountNo string `json:"bankAccountNo"`
	BankIFSC      string `json:"bankIfsc"`
}
type UpdateAgentGoalPayload struct {
	TargetIncome *float64 `json:"targetIncome"` // Use pointer for optional update
	TargetPeriod string   `json:"targetPeriod"`
}
type AgentInsurerDetail struct {
	ID                   int64           `json:"id,omitempty"`
	AgentUserID          int64           `json:"-"`
	InsurerName          string          `json:"insurerName"`
	AgentCode            sql.NullString  `json:"agentCode"`
	SpocEmail            sql.NullString  `json:"spocEmail"`
	CommissionPercentage sql.NullFloat64 `json:"commissionPercentage"` // General/Default rate
}

// Updated struct for GET /api/agents/profile response
type FullAgentProfileWithDetails struct {
	User                                // Embed basic user info
	AgentProfile                        // Embed extended profile info
	InsurerDetails []AgentInsurerDetail `json:"insurerDetails"` // Changed from InsurerPOCs
}

func createClient(client Client) (int64, error) {
	log.Printf("DATABASE: Creating client '%s' for agent %d\n", client.Name, client.AgentUserID)
	stmt, err := db.Prepare(`INSERT INTO clients (
        agent_user_id, name, email, phone, dob, address, status, tags, last_contacted_at,
        income, marital_status, city, job_profile, dependents, liability, housing_type,
        vehicle_count, vehicle_type, vehicle_cost, created_at
        ) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("failed to prepare insert client statement: %w", err)
	}
	defer stmt.Close()

	res, err := stmt.Exec(
		client.AgentUserID, client.Name, client.Email, client.Phone, client.Dob, client.Address,
		client.Status, client.Tags, client.LastContactedAt,
		client.Income, client.MaritalStatus, client.City, client.JobProfile, client.Dependents,
		client.Liability, client.HousingType, client.VehicleCount, client.VehicleType, client.VehicleCost,
		time.Now(), // Set created_at
	)
	if err != nil {
		return 0, fmt.Errorf("failed to execute insert client: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get last insert ID: %w", err)
	}
	log.Printf("DATABASE: Client created with ID: %d\n", id)
	return id, nil
}

// Updated getClientByID to select new fields
func getClientByID(clientID int64, agentUserID int64) (*Client, error) {
	log.Printf("DATABASE: Getting client ID %d for agent %d\n", clientID, agentUserID)
	row := db.QueryRow(`SELECT
        id, agent_user_id, name, email, phone, dob, address, status, tags, last_contacted_at, created_at,
        income, marital_status, city, job_profile, dependents, liability, housing_type,
        vehicle_count, vehicle_type, vehicle_cost
        FROM clients WHERE id = ? AND agent_user_id = ?`, clientID, agentUserID)
	client := &Client{}
	err := row.Scan(
		&client.ID, &client.AgentUserID, &client.Name, &client.Email, &client.Phone, &client.Dob, &client.Address,
		&client.Status, &client.Tags, &client.LastContactedAt, &client.CreatedAt,
		&client.Income, &client.MaritalStatus, &client.City, &client.JobProfile, &client.Dependents,
		&client.Liability, &client.HousingType, &client.VehicleCount, &client.VehicleType, &client.VehicleCost,
	)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("ERROR: Failed to scan client row: %v\n", err)
		} else {
			log.Printf("DATABASE: Client %d not found or not owned by agent %d\n", clientID, agentUserID)
		}
		return nil, err
	}
	return client, nil
}

// Updated updateClient to include new fields
func updateClient(clientID int64, agentUserID int64, client Client) error {
	log.Printf("DATABASE: Updating client ID %d for agent %d\n", clientID, agentUserID)
	client.LastContactedAt = sql.NullTime{Time: time.Now(), Valid: true} // Always update last contacted on update
	stmt, err := db.Prepare(`UPDATE clients SET
        name = ?, email = ?, phone = ?, dob = ?, address = ?, status = ?, tags = ?, last_contacted_at = ?,
        income = ?, marital_status = ?, city = ?, job_profile = ?, dependents = ?, liability = ?, housing_type = ?,
        vehicle_count = ?, vehicle_type = ?, vehicle_cost = ?
        WHERE id = ? AND agent_user_id = ?`)
	if err != nil {
		return fmt.Errorf("failed to prepare update client statement: %w", err)
	}
	defer stmt.Close()

	res, err := stmt.Exec(
		client.Name, client.Email, client.Phone, client.Dob, client.Address, client.Status, client.Tags, client.LastContactedAt,
		client.Income, client.MaritalStatus, client.City, client.JobProfile, client.Dependents, client.Liability, client.HousingType,
		client.VehicleCount, client.VehicleType, client.VehicleCost,
		clientID, agentUserID,
	)
	if err != nil {
		return fmt.Errorf("failed to execute update client: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return sql.ErrNoRows
	} // Indicate if no row was updated (wrong ID or agent)
	log.Printf("DATABASE: Client %d updated successfully by agent %d\n", clientID, agentUserID)
	return nil
}

// --- Database Functions ---
func setupDatabase() error {
	log.Println("DATABASE: Setting up MySQL database...")
	var err error
	// The MySQL connection string will use config.DBDSN
	db, err = sql.Open("mysql", config.DBDSN) // Use the DSN from your config
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	if err = db.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}
	execSQL := func(sql string, tableName string) error {
		_, err := db.Exec(sql)
		if err != nil {
			return fmt.Errorf("failed to create %s table: %w", tableName, err)
		}
		log.Printf("DATABASE: '%s' table checked/created.\n", tableName)
		return nil
	}

	// Create All Tables...
	if err := execSQL(`CREATE TABLE IF NOT EXISTS users (
        id INT PRIMARY KEY AUTO_INCREMENT,
        email VARCHAR(255) NOT NULL UNIQUE,
        password_hash VARCHAR(255) NOT NULL,
        user_type VARCHAR(10) NOT NULL CHECK(user_type IN ('agent', 'agency')),
        is_verified BOOLEAN NOT NULL DEFAULT 0,
        created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
    ) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`, "users"); err != nil {
		return err
	}

	if err := execSQL(`CREATE TABLE IF NOT EXISTS tokens (
        user_id INT NOT NULL,
        token_hash VARCHAR(255) NOT NULL,
        purpose VARCHAR(20) NOT NULL CHECK(purpose IN ('verification', 'reset')),
        expires_at TIMESTAMP NOT NULL,
        PRIMARY KEY (user_id, purpose),
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
    ) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`, "tokens"); err != nil {
		return err
	}

	if err := execSQL(`CREATE TABLE IF NOT EXISTS notices (
        id INT PRIMARY KEY AUTO_INCREMENT,
        title VARCHAR(255) NOT NULL,
        content TEXT NOT NULL,
        category VARCHAR(100),
        posted_by VARCHAR(100),
        is_important BOOLEAN NOT NULL DEFAULT 0,
        created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
    ) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`, "notices"); err != nil {
		return err
	}

	if err := execSQL(`CREATE TABLE IF NOT EXISTS clients (
        id INT PRIMARY KEY AUTO_INCREMENT,
        agent_user_id INT NOT NULL,
        name VARCHAR(255) NOT NULL,
        email VARCHAR(255),
        phone VARCHAR(50),
        dob VARCHAR(20), -- Consider DATE type if format is guaranteed
        address TEXT,
        status VARCHAR(20) CHECK(status IN ('Lead', 'Active', 'Lapsed')) NOT NULL,
        tags TEXT,
        last_contacted_at TIMESTAMP NULL DEFAULT NULL, -- Explicitly allow NULL
        created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
        income DECIMAL(15, 2),
        marital_status VARCHAR(50),
        city VARCHAR(100),
        job_profile VARCHAR(100),
        dependents INT,
        liability DECIMAL(15, 2),
        housing_type VARCHAR(50),
        vehicle_count INT,
        vehicle_type VARCHAR(100),
        vehicle_cost DECIMAL(15, 2),
        UNIQUE(agent_user_id, email),
        UNIQUE(agent_user_id, phone),
        FOREIGN KEY (agent_user_id) REFERENCES users(id) ON DELETE CASCADE
    ) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`, "clients"); err != nil {
		return err
	}

	if err := execSQL(`CREATE TABLE IF NOT EXISTS products (
        id VARCHAR(100) PRIMARY KEY, -- Assuming product IDs are like "PROD-XYZ"
        name VARCHAR(255) NOT NULL,
        category VARCHAR(100) NOT NULL,
        insurer VARCHAR(100) NOT NULL,
        description TEXT,
        status VARCHAR(50) DEFAULT 'Active',
        features TEXT,
        eligibility TEXT,
        term VARCHAR(100),
        exclusions TEXT,
        room_rent VARCHAR(100),
        premium_indication VARCHAR(255),
        insurer_logo_url VARCHAR(2083), -- Max URL length
        brochure_url VARCHAR(2083),
        wording_url VARCHAR(2083),
        claim_form_url VARCHAR(2083),
        upfront_commission_percentage DOUBLE DEFAULT 0.0,
        trail_commission_percentage DOUBLE DEFAULT 0.0,
        created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
        updated_at TIMESTAMP NULL DEFAULT NULL ON UPDATE CURRENT_TIMESTAMP -- Auto-updates on modification
    ) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`, "products"); err != nil {
		return err
	}

	if err := execSQL(`CREATE TABLE IF NOT EXISTS policies (
        id VARCHAR(100) PRIMARY KEY, -- Assuming policy IDs are like "POL-XYZ"
        client_id INT NOT NULL,
        agent_user_id INT NOT NULL,
        product_id VARCHAR(100),
        policy_number VARCHAR(100) NOT NULL,
        insurer VARCHAR(100),
        premium DECIMAL(15, 2),
        sum_insured DECIMAL(15, 2),
        start_date VARCHAR(20), -- Consider DATE type
        end_date VARCHAR(20),   -- Consider DATE type
        status VARCHAR(50),
        policy_doc_url VARCHAR(2083),
        upfront_commission_amount DECIMAL(15, 2) DEFAULT 0.0,
        created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
        updated_at TIMESTAMP NULL DEFAULT NULL ON UPDATE CURRENT_TIMESTAMP,
        FOREIGN KEY (client_id) REFERENCES clients(id) ON DELETE CASCADE,
        FOREIGN KEY (agent_user_id) REFERENCES users(id) ON DELETE CASCADE,
        FOREIGN KEY (product_id) REFERENCES products(id) ON DELETE SET NULL
    ) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`, "policies"); err != nil {
		return err
	}

	if err := execSQL(`CREATE TABLE IF NOT EXISTS communications (
        id INT PRIMARY KEY AUTO_INCREMENT,
        client_id INT NOT NULL,
        agent_user_id INT NOT NULL,
        type VARCHAR(50),
        timestamp TIMESTAMP NULL, -- Store actual timestamp from interaction
        summary TEXT,
        created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, -- Record creation time
        FOREIGN KEY (client_id) REFERENCES clients(id) ON DELETE CASCADE,
        FOREIGN KEY (agent_user_id) REFERENCES users(id) ON DELETE CASCADE
    ) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`, "communications"); err != nil {
		return err
	}

	if err := execSQL(`CREATE TABLE IF NOT EXISTS tasks (
        id INT PRIMARY KEY AUTO_INCREMENT,
        client_id INT NOT NULL, -- Assuming tasks are always client-specific
        agent_user_id INT NOT NULL,
        description TEXT NOT NULL,
        due_date VARCHAR(20), -- Consider DATE type
        is_urgent BOOLEAN DEFAULT 0,
        is_completed BOOLEAN DEFAULT 0,
        created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
        completed_at TIMESTAMP NULL DEFAULT NULL,
        FOREIGN KEY (client_id) REFERENCES clients(id) ON DELETE CASCADE,
        FOREIGN KEY (agent_user_id) REFERENCES users(id) ON DELETE CASCADE
    ) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`, "tasks"); err != nil {
		return err
	}

	if err := execSQL(`CREATE TABLE IF NOT EXISTS documents (
        id INT PRIMARY KEY AUTO_INCREMENT,
        client_id INT NOT NULL,
        agent_user_id INT NOT NULL,
        title VARCHAR(255),
        document_type VARCHAR(100),
        file_url VARCHAR(2083) NOT NULL,
        uploaded_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
        FOREIGN KEY (client_id) REFERENCES clients(id) ON DELETE CASCADE,
        FOREIGN KEY (agent_user_id) REFERENCES users(id) ON DELETE CASCADE
    ) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`, "documents"); err != nil {
		return err
	}

	if err := execSQL(`CREATE TABLE IF NOT EXISTS marketing_campaigns (
        id INT PRIMARY KEY AUTO_INCREMENT,
        agent_user_id INT NOT NULL,
        name VARCHAR(255) NOT NULL,
        status VARCHAR(50),
        target_segment_name VARCHAR(255),
        sent_at TIMESTAMP NULL DEFAULT NULL,
        stats_opens INT DEFAULT 0,
        stats_clicks INT DEFAULT 0,
        stats_leads INT DEFAULT 0,
        created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
        FOREIGN KEY (agent_user_id) REFERENCES users(id) ON DELETE CASCADE
    ) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`, "marketing_campaigns"); err != nil {
		return err
	}

	if err := execSQL(`CREATE TABLE IF NOT EXISTS marketing_templates (
        id INT PRIMARY KEY AUTO_INCREMENT,
        name VARCHAR(255) NOT NULL,
        type VARCHAR(50),
        category VARCHAR(100),
        preview_text TEXT,
        content MEDIUMTEXT, -- For potentially large HTML email templates
        created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
    ) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`, "marketing_templates"); err != nil {
		return err
	}

	if err := execSQL(`CREATE TABLE IF NOT EXISTS marketing_content (
        id INT PRIMARY KEY AUTO_INCREMENT,
        title VARCHAR(255) NOT NULL,
        content_type VARCHAR(50),
        description TEXT,
        gcs_url VARCHAR(2083) NOT NULL,
        thumbnail_url VARCHAR(2083),
        created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
    ) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`, "marketing_content"); err != nil {
		return err
	}

	if err := execSQL(`CREATE TABLE IF NOT EXISTS client_segments (
        id INT PRIMARY KEY AUTO_INCREMENT,
        agent_user_id INT NOT NULL,
        name VARCHAR(255) NOT NULL,
        criteria TEXT,
        client_count INT,
        created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
        FOREIGN KEY (agent_user_id) REFERENCES users(id) ON DELETE CASCADE
    ) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`, "client_segments"); err != nil {
		return err
	}

	if err := execSQL(`CREATE TABLE IF NOT EXISTS activity_log (
        id INT PRIMARY KEY AUTO_INCREMENT,
        agent_user_id INT NOT NULL,
        timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
        activity_type VARCHAR(100) NOT NULL,
        description TEXT NOT NULL,
        related_id VARCHAR(100), -- If related ID can be non-integer
        FOREIGN KEY (agent_user_id) REFERENCES users(id) ON DELETE CASCADE
    ) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`, "activity_log"); err != nil {
		return err
	}

	// The agent_insurer_pocs table is created here, but then dropped below.
	// This seems like an evolution of the schema.
	// If agent_insurer_relations is the final table, agent_insurer_pocs might not be needed.
	if err := execSQL(`CREATE TABLE IF NOT EXISTS agent_insurer_pocs (
            id INT PRIMARY KEY AUTO_INCREMENT,
            agent_user_id INT NOT NULL,
            insurer_name VARCHAR(255) NOT NULL,
            poc_email VARCHAR(255) NOT NULL,
            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
            FOREIGN KEY (agent_user_id) REFERENCES users(id) ON DELETE CASCADE,
            UNIQUE(agent_user_id, insurer_name)
        ) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`, "agent_insurer_pocs"); err != nil {
		return err
	}

	if err := execSQL(`CREATE TABLE IF NOT EXISTS client_portal_tokens (
        token VARCHAR(255) PRIMARY KEY,
        client_id INT NOT NULL,
        agent_user_id INT NOT NULL,
        expires_at TIMESTAMP NOT NULL,
        created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
        FOREIGN KEY (client_id) REFERENCES clients(id) ON DELETE CASCADE,
        FOREIGN KEY (agent_user_id) REFERENCES users(id) ON DELETE CASCADE
    ) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`, "client_portal_tokens"); err != nil {
		return err
	}

	// Index creation syntax is generally compatible
	var count int
	err = db.QueryRow(`
		SELECT COUNT(1)
		FROM INFORMATION_SCHEMA.STATISTICS
		WHERE TABLE_SCHEMA = DATABASE()
		AND TABLE_NAME = 'client_portal_tokens'
		AND INDEX_NAME = 'idx_client_portal_tokens_expiry';
	`).Scan(&count)
	if err != nil {
		log.Printf("WARN: Could not check for existing index: %v", err)
	} else if count > 0 {
		if err := execSQL(`ALTER TABLE client_portal_tokens DROP INDEX idx_client_portal_tokens_expiry`, "idx_client_portal_tokens_expiry_drop"); err != nil {
			log.Printf("WARN: Failed to drop existing index idx_client_portal_tokens_expiry: %v", err)
		}
	}

	if err := execSQL(`CREATE INDEX idx_client_portal_tokens_expiry ON client_portal_tokens (expires_at)`, "idx_client_portal_tokens_expiry_create"); err != nil {
		return fmt.Errorf("failed to create index idx_client_portal_tokens_expiry: %w", err)
	}

	if err := execSQL(`CREATE TABLE IF NOT EXISTS agent_profiles (
        user_id INT PRIMARY KEY,
        mobile VARCHAR(50),
        gender VARCHAR(20),
        postal_address TEXT,
        agency_name VARCHAR(255),
        pan VARCHAR(20) UNIQUE,
        bank_name VARCHAR(100),
        bank_account_no VARCHAR(50),
        bank_ifsc VARCHAR(20),
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
    ) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`, "agent_profiles"); err != nil {
		return err
	}

	// The agent_insurer_details table is created here, but then dropped below.
	// This also seems like an evolution of the schema.
	if err := execSQL(`CREATE TABLE IF NOT EXISTS agent_insurer_details (
        id INT PRIMARY KEY AUTO_INCREMENT,
        agent_user_id INT NOT NULL,
        insurer_name VARCHAR(255) NOT NULL,
        agent_code VARCHAR(100),
        spoc_email VARCHAR(255),
        commission_percentage DOUBLE,
        created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
        FOREIGN KEY (agent_user_id) REFERENCES users(id) ON DELETE CASCADE,
        UNIQUE(agent_user_id, insurer_name)
    ) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`, "agent_insurer_details"); err != nil {
		return err
	}

	// WARNING: These DROP TABLE statements will remove the tables immediately after creation if they exist.
	// If agent_insurer_relations is the intended final schema for insurer contact/commission details,
	// then agent_insurer_pocs and agent_insurer_details might be obsolete.
	// If these drops are for cleanup before creating a definitive version, they should be placed earlier.
	// I am keeping them as per your original code snippet's structure.
	_, _ = db.Exec("DROP TABLE IF EXISTS agent_insurer_pocs;")
	_, _ = db.Exec("DROP TABLE IF EXISTS agent_insurer_details;")
	log.Println("DATABASE: Dropped old insurer contact tables (agent_insurer_pocs, agent_insurer_details) if they existed.")

	// This agent_insurer_relations table seems to be the most comprehensive version.
	if err := execSQL(`CREATE TABLE IF NOT EXISTS agent_insurer_relations (
        id INT PRIMARY KEY AUTO_INCREMENT,
        agent_user_id INT NOT NULL,
        insurer_name VARCHAR(255) NOT NULL,
        agent_code VARCHAR(100),
        spoc_email VARCHAR(255),
        upfront_commission_percentage DOUBLE,
        trail_commission_percentage DOUBLE,
        name VARCHAR(255) NOT NULL, -- This seems to be Product Name
        category VARCHAR(100) NOT NULL, -- This seems to be Product Category
        description TEXT,
        status VARCHAR(50) NOT NULL, -- Product status
        features TEXT,
        eligibility TEXT,
        term VARCHAR(100),
        exclusions TEXT,
        room_rent VARCHAR(100),
        premium_indication VARCHAR(255),
        insurer_logo_url VARCHAR(2083),
        brochure_url VARCHAR(2083),
        wording_url VARCHAR(2083),
        claim_form_url VARCHAR(2083),
        product_id VARCHAR(100), -- Reference to the products table's ID
        created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
        updated_at TIMESTAMP NULL DEFAULT NULL ON UPDATE CURRENT_TIMESTAMP,
        FOREIGN KEY (agent_user_id) REFERENCES users(id) ON DELETE CASCADE,
        -- Consider adding FOREIGN KEY (product_id) REFERENCES products(id) if product_id here must exist in products table
        UNIQUE(agent_user_id, insurer_name, product_id) -- More likely unique constraint
    ) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`, "agent_insurer_relations"); err != nil {
		return err
	}

	if err := execSQL(`CREATE TABLE IF NOT EXISTS agent_goals (
        user_id INT PRIMARY KEY,
        target_income DECIMAL(15, 2),
        target_period VARCHAR(50), -- e.g., "2025-Q2", "2025-Annual"
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
    ) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`, "agent_goals"); err != nil {
		return err
	}

	log.Println("DATABASE: Setup complete.")
	return nil
}
func createUser(user User) (int64, error) {
	stmt, err := db.Prepare("INSERT INTO users(email, password_hash, user_type, is_verified) VALUES(?, ?, ?, ?)")
	if err != nil {
		return 0, fmt.Errorf("failed to prepare insert user statement: %w", err)
	}
	defer stmt.Close()
	res, err := stmt.Exec(user.Email, user.PasswordHash, user.UserType, user.IsVerified)
	if err != nil {
		return 0, fmt.Errorf("failed to execute insert user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get last insert ID: %w", err)
	}
	log.Printf("DATABASE: User created with ID: %d\n", id)
	return id, nil
}

func getUserByEmail(email string) (*User, error) {
	row := db.QueryRow("SELECT id, email, password_hash, user_type, is_verified, created_at FROM users WHERE email = ?", email)
	user := &User{}
	err := row.Scan(&user.ID, &user.Email, &user.PasswordHash, &user.UserType, &user.IsVerified, &user.CreatedAt)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("ERROR: Failed to scan user row: %v\n", err)
		} else {
			log.Printf("DATABASE: User not found: %s\n", email)
		}
		return nil, err
	}
	return user, nil
}

func parseFloatOrNull(s string) sql.NullFloat64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || s == "" {
		return sql.NullFloat64{Valid: false}
	}
	return sql.NullFloat64{Float64: f, Valid: true}
}

// Helper function to safely parse optional int from string
func parseIntOrNull(s string) sql.NullInt64 {
	i, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || s == "" {
		return sql.NullInt64{Valid: false}
	}
	return sql.NullInt64{Int64: i, Valid: true}
}

func storeToken(userID int64, token string, purpose string, duration time.Duration) error {
	hashedToken, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash1 token: %w", err)
	}
	expiresAt := time.Now().Add(duration)
	stmt, err := db.Prepare("INSERT INTO tokens(user_id, token_hash, purpose, expires_at) VALUES(?, ?, ?, ?) ON DUPLICATE KEY UPDATE token_hash = VALUES(token_hash), purpose = VALUES(purpose), expires_at = VALUES(expires_at)")
	print("topen error%w", stmt)
	if err != nil {
		return fmt.Errorf("failed to prepare store token statement: %w", err)
	}
	defer stmt.Close()
	_, err = stmt.Exec(userID, string(hashedToken), purpose, expiresAt)
	if err != nil {
		return fmt.Errorf("failed to execute store token: %w", err)
	}
	log.Printf("DATABASE: Token stored successfully for user %d, purpose %s\n", userID, purpose)
	return nil
}

func verifyToken(token string, purpose string) (userID int64, err error) {
	rows, err := db.Query("SELECT user_id, token_hash FROM tokens WHERE purpose = ? AND expires_at > ?", purpose, time.Now())
	if err != nil {
		log.Printf("ERROR: Failed to query tokens: %v\n", err)
		return 0, fmt.Errorf("database query error")
	}
	defer rows.Close()
	var dbUserID int64
	var dbTokenHash string
	found := false
	for rows.Next() {
		if err := rows.Scan(&dbUserID, &dbTokenHash); err != nil {
			log.Printf("ERROR: Failed to scan token row: %v\n", err)
			continue
		}
		err = bcrypt.CompareHashAndPassword([]byte(dbTokenHash), []byte(token))
		if err == nil {
			found = true
			userID = dbUserID
			log.Printf("DATABASE: Token verified for user ID %d\n", userID)
			break
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("ERROR: Error iterating token rows: %v\n", err)
		return 0, fmt.Errorf("database iteration error")
	}
	if !found {
		log.Printf("DATABASE: Token not found or invalid/expired\n")
		return 0, sql.ErrNoRows
	}
	return userID, nil
}

func getClientSegmentByID(segmentID int64, agentUserID int64) (*ClientSegment, error) {
	log.Printf("DATABASE: Getting segment %d for agent %d\n", segmentID, agentUserID)
	row := db.QueryRow(`SELECT id, agent_user_id, name, criteria, client_count, created_at
                       FROM client_segments WHERE id = ? AND agent_user_id = ?`, segmentID, agentUserID)
	segment := &ClientSegment{}
	err := row.Scan(
		&segment.ID, &segment.AgentUserID, &segment.Name, &segment.Criteria,
		&segment.ClientCount, &segment.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, sql.ErrNoRows
		} // Not found or not owned
		log.Printf("ERROR: Failed to scan segment row %d: %v\n", segmentID, err)
		return nil, err
	}
	return segment, nil
}

// NEW: DB Function to update a client segment
func updateClientSegment(segment ClientSegment) error {
	log.Printf("DATABASE: Updating segment %d for agent %d\n", segment.ID, segment.AgentUserID)
	stmt, err := db.Prepare(`UPDATE client_segments SET name = ?, criteria = ?
                           WHERE id = ? AND agent_user_id = ?`)
	if err != nil {
		return fmt.Errorf("failed to prepare update segment: %w", err)
	}
	defer stmt.Close()

	res, err := stmt.Exec(segment.Name, segment.Criteria, segment.ID, segment.AgentUserID)
	if err != nil {
		return fmt.Errorf("failed to execute update segment: %w", err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return sql.ErrNoRows
	} // Indicate not found or wrong owner

	log.Printf("DATABASE: Segment %d updated successfully\n", segment.ID)
	return nil
}
func markUserVerified(userID int64) error {
	stmt, err := db.Prepare("UPDATE users SET is_verified = 1 WHERE id = ?")
	if err != nil {
		return fmt.Errorf("failed to prepare verify user statement: %w", err)
	}
	defer stmt.Close()
	_, err = stmt.Exec(userID)
	if err != nil {
		return fmt.Errorf("failed to execute verify user: %w", err)
	}
	log.Printf("DATABASE: User %d marked as verified\n", userID)
	return nil
}

func updateUserPassword(userID int64, newPasswordHash string) error {
	stmt, err := db.Prepare("UPDATE users SET password_hash = ? WHERE id = ?")
	if err != nil {
		return fmt.Errorf("failed to prepare update password statement: %w", err)
	}
	defer stmt.Close()
	_, err = stmt.Exec(newPasswordHash, userID)
	if err != nil {
		return fmt.Errorf("failed to execute update password: %w", err)
	}
	log.Printf("DATABASE: Password updated for user %d\n", userID)
	return nil
}
func getAllClientTasks(clientID int64, agentUserID int64) ([]Task, error) {
	log.Printf("DATABASE: Fetching ALL tasks for client %d (agent %d)\n", clientID, agentUserID)
	// Fetch ALL tasks, order by creation date or due date
	rows, err := db.Query(`SELECT id, client_id, agent_user_id, description, due_date, is_urgent, is_completed, created_at, completed_at
						   FROM tasks WHERE client_id = ? AND agent_user_id = ?
						   ORDER BY created_at DESC`, clientID, agentUserID)
	if err != nil {
		log.Printf("ERROR: Query all tasks failed: %v", err)
		return nil, err
	}
	defer rows.Close()
	var tasks []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.ClientID, &t.AgentUserID, &t.Description, &t.DueDate, &t.IsUrgent, &t.IsCompleted, &t.CreatedAt, &t.CompletedAt); err != nil {
			log.Printf("ERROR: Scan all tasks row failed: %v", err)
			continue
		}
		tasks = append(tasks, t)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return tasks, nil
}

type MonthlySalesData struct {
	Month *string `json:"month"` // Changed to *string
	Count int     `json:"count"`
}

func getMonthlyPolicyCount(agentUserID int64, months int) ([]MonthlySalesData, error) {
	log.Printf("DATABASE: Fetching monthly policy counts for agent %d (last %d months)\n", agentUserID, months)
	// Calculate the date 'months' ago from the start of the current month
	firstOfMonth := time.Date(time.Now().Year(), time.Now().Month(), 1, 0, 0, 0, 0, time.UTC)
	startDate := firstOfMonth.AddDate(0, -months, 0)

	query := `
		SELECT DATE_FORMAT(start_date, '%Y-%m') as month, COUNT(*) as count
		FROM policies
		WHERE agent_user_id = ? AND start_date >= ?
		GROUP BY month
		ORDER BY month ASC
		LIMIT ?;
	`
	// Limit ensures we don't exceed the number of months requested,
	// even if data spans longer (e.g., if 'months' is 6 but data exists for 12)
	rows, err := db.Query(query, agentUserID, startDate, months)
	if err != nil {
		log.Printf("ERROR: Query monthly policy count failed: %v", err)
		return nil, err
	}
	defer rows.Close()

	var results []MonthlySalesData
	for rows.Next() {
		var data MonthlySalesData
		if err := rows.Scan(&data.Month, &data.Count); err != nil {
			log.Printf("ERROR: Scan monthly policy count row failed: %v", err)
			continue
		}
		results = append(results, data)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	log.Printf("DATABASE: Found %d months of policy data for agent %d.\n", len(results), agentUserID)
	return results, nil
}

func deleteTokenByUserID(userID int64, purpose string) error {
	stmt, err := db.Prepare("DELETE FROM tokens WHERE user_id = ? AND purpose = ?")
	if err != nil {
		return fmt.Errorf("failed to prepare delete token statement: %w", err)
	}
	defer stmt.Close()
	_, err = stmt.Exec(userID, purpose)
	if err != nil {
		return fmt.Errorf("failed to execute delete token: %w", err)
	}
	log.Printf("DATABASE: Token deleted for user %d, purpose %s\n", userID, purpose)
	return nil
}

func getNotices(categoryFilter string) ([]Notice, error) {
	query := "SELECT id, title, content, category, posted_by, is_important, created_at FROM notices"
	args := []interface{}{}
	if categoryFilter != "" && categoryFilter != "All Categories" {
		query += " WHERE category = ?"
		args = append(args, categoryFilter)
	}
	query += " ORDER BY created_at DESC"
	rows, err := db.Query(query, args...)
	if err != nil {
		log.Printf("ERROR: Failed to query notices: %v\n", err)
		return nil, fmt.Errorf("database query error")
	}
	defer rows.Close()
	notices := []Notice{}
	for rows.Next() {
		var n Notice
		var createdAtStr string
		var category sql.NullString
		var postedBy sql.NullString
		if err := rows.Scan(&n.ID, &n.Title, &n.Content, &category, &postedBy, &n.IsImportant, &createdAtStr); err != nil {
			log.Printf("ERROR: Failed to scan notice row: %v\n", err)
			continue
		}
		if category.Valid {
			n.Category = category.String
		}
		if postedBy.Valid {
			n.PostedBy = postedBy.String
		}
		layout := "2006-01-02 15:04:05"
		parsedTime, err := time.Parse(layout, createdAtStr)
		if err != nil {
			parsedTime, err = time.Parse(time.RFC3339, createdAtStr)
			if err != nil {
				log.Printf("WARN: Failed to parse timestamp '%s' for notice %d: %v", createdAtStr, n.ID, err)
			}
		}
		n.CreatedAt = parsedTime
		notices = append(notices, n)
	}
	if err := rows.Err(); err != nil {
		log.Printf("ERROR: Error iterating notice rows: %v\n", err)
		return nil, fmt.Errorf("database iteration error")
	}
	log.Printf("DATABASE: Found %d notices.\n", len(notices))
	return notices, nil
}
func fetchAiRecommendationForClient(client Client, estimation CoverageEstimation) (string, error) {
	log.Printf("AI RECOMMENDATION: Fetching for client %d", client.ID)
	// if config.GoogleAiApiKey == "" {
	// 	return "", errors.New("AI service is not configured")
	// }
	const GOOGLE_AI_API_KEY = "AIzaSyAoIOupDd4VBbcJMob0tTlaiGOTsP3AqXg" // <<< REPLACE FOR TESTING ONLY
	//
	// Construct Prompt (similar to the one used in ClientProfilePage frontend, but now in backend)
	age := calculateAge(client.Dob.String)
	ageStr := "N/A"
	if age > 0 {
		ageStr = strconv.Itoa(age)
	}
	incomeStr := "N/A"
	if client.Income.Valid {
		incomeStr = fmt.Sprintf("₹%.0f/year", client.Income.Float64)
	}
	dependentsStr := "N/A"
	if client.Dependents.Valid {
		dependentsStr = strconv.FormatInt(client.Dependents.Int64, 10)
	}

	promptText := fmt.Sprintf("Analyze this insurance client profile: Age %s, City %s, Income %s, Marital Status %s, Dependents %s. Current estimated coverage needs are Health: %.1f %s, Life: %.2f %s, Motor: %.0f %s. Based ONLY on this information, provide a brief (1-2 paragraph) recommendation focusing on potential coverage gaps or areas the client might consider discussing further with their agent. Avoid specific product names. Be encouraging.",
		ageStr, client.City.String, incomeStr, client.MaritalStatus.String, dependentsStr,
		estimation.Health.Amount, estimation.Health.Unit,
		estimation.Life.Amount, estimation.Life.Unit,
		estimation.Motor.Amount, estimation.Motor.Unit,
	)

	// Call Gemini API
	geminiURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-1.5-flash:generateContent?key=%s", GOOGLE_AI_API_KEY)
	requestPayload := GeminiRequest{
		Contents:         []GeminiContent{{Parts: []GeminiPart{{Text: promptText}}}},
		GenerationConfig: &GeminiGenerationConfig{Temperature: 0.7, MaxOutputTokens: 250},
	}
	payloadBytes, err := json.Marshal(requestPayload)
	if err != nil {
		return "", fmt.Errorf("marshalling Gemini request failed: %w", err)
	}
	resp, err := http.Post(geminiURL, "application/json", bytes.NewBuffer(payloadBytes))
	if err != nil {
		return "", fmt.Errorf("calling Gemini API failed: %w", err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading Gemini response failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		log.Printf("ERROR: Gemini API non-OK status: %d, Body: %s", resp.StatusCode, string(bodyBytes))
		return "", fmt.Errorf("AI service returned error: %s", resp.Status)
	}

	// Parse Response
	var geminiResp GeminiResponse
	if err := json.Unmarshal(bodyBytes, &geminiResp); err != nil {
		log.Printf("ERROR: Unmarshalling Gemini response: %v\nBody: %s", err, string(bodyBytes))
		return "", errors.New("error parsing AI response")
	}
	if len(geminiResp.Candidates) > 0 && len(geminiResp.Candidates[0].Content.Parts) > 0 {
		aiText := geminiResp.Candidates[0].Content.Parts[0].Text
		log.Printf("AI RECOMMENDATION: Received for client %d", client.ID)
		return aiText, nil
	}
	return "", errors.New("no recommendation text found in AI response")
}

// func createClient(client Client) (int64, error) {
// 	stmt, err := db.Prepare(`INSERT INTO clients (agent_user_id, name, email, phone, dob, address, status, tags, last_contacted_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`)
// 	if err != nil {
// 		return 0, fmt.Errorf("failed to prepare insert client statement: %w", err)
// 	}
// 	defer stmt.Close()
// 	res, err := stmt.Exec(client.AgentUserID, client.Name, client.Email, client.Phone, client.Dob, client.Address, client.Status, client.Tags, client.LastContactedAt)
// 	if err != nil {
// 		return 0, fmt.Errorf("failed to execute insert client: %w", err)
// 	}
// 	id, err := res.LastInsertId()
// 	if err != nil {
// 		return 0, fmt.Errorf("failed to get last insert ID: %w", err)
// 	}
// 	log.Printf("DATABASE: Client created with ID: %d\n", id)
// 	return id, nil
// }

func getClientsByAgentID(agentUserID int64, statusFilter, searchTerm string, limit, offset int) ([]Client, error) {
	query := `SELECT id, agent_user_id, name, email, phone, dob, address, status, tags, last_contacted_at, created_at FROM clients WHERE agent_user_id = ?`
	args := []interface{}{agentUserID}
	if statusFilter != "" && statusFilter != "All Statuses" {
		query += " AND status = ?"
		args = append(args, statusFilter)
	}
	if searchTerm != "" {
		query += " AND (name LIKE ? OR email LIKE ? OR phone LIKE ?)"
		term := "%" + searchTerm + "%"
		args = append(args, term, term, term)
	}
	query += " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, err := db.Query(query, args...)
	if err != nil {
		log.Printf("ERROR: Failed to query clients: %v\n", err)
		return nil, fmt.Errorf("database query error")
	}
	defer rows.Close()
	clients := []Client{}
	for rows.Next() {
		var c Client
		if err := rows.Scan(&c.ID, &c.AgentUserID, &c.Name, &c.Email, &c.Phone, &c.Dob, &c.Address, &c.Status, &c.Tags, &c.LastContactedAt, &c.CreatedAt); err != nil {
			log.Printf("ERROR: Failed to scan client row: %v\n", err)
			continue
		}
		clients = append(clients, c)
	}
	if err := rows.Err(); err != nil {
		log.Printf("ERROR: Error iterating client rows: %v\n", err)
		return nil, fmt.Errorf("database iteration error")
	}
	log.Printf("DATABASE: Found %d clients for agent %d.\n", len(clients), agentUserID)
	return clients, nil
}

// func getClientByID(clientID int64, agentUserID int64) (*Client, error) {
// 	row := db.QueryRow(`SELECT id, agent_user_id, name, email, phone, dob, address, status, tags, last_contacted_at, created_at FROM clients WHERE id = ? AND agent_user_id = ?`, clientID, agentUserID)
// 	client := &Client{}
// 	err := row.Scan(&client.ID, &client.AgentUserID, &client.Name, &client.Email, &client.Phone, &client.Dob, &client.Address, &client.Status, &client.Tags, &client.LastContactedAt, &client.CreatedAt)
// 	if err != nil {
// 		if err != sql.ErrNoRows {
// 			log.Printf("ERROR: Failed to scan client row: %v\n", err)
// 		} else {
// 			log.Printf("DATABASE: Client %d not found or not owned by agent %d\n", clientID, agentUserID)
// 		}
// 		return nil, err
// 	}
// 	return client, nil
// }

//	func updateClient(clientID int64, agentUserID int64, client Client) error {
//		client.LastContactedAt = sql.NullTime{Time: time.Now(), Valid: true}
//		stmt, err := db.Prepare(`UPDATE clients SET name = ?, email = ?, phone = ?, dob = ?, address = ?, status = ?, tags = ?, last_contacted_at = ? WHERE id = ? AND agent_user_id = ?`)
//		if err != nil {
//			return fmt.Errorf("failed to prepare update client statement: %w", err)
//		}
//		defer stmt.Close()
//		res, err := stmt.Exec(client.Name, client.Email, client.Phone, client.Dob, client.Address, client.Status, client.Tags, client.LastContactedAt, clientID, agentUserID)
//		if err != nil {
//			return fmt.Errorf("failed to execute update client: %w", err)
//		}
//		rowsAffected, err := res.RowsAffected()
//		if err != nil {
//			return fmt.Errorf("failed to get rows affected: %w", err)
//		}
//		if rowsAffected == 0 {
//			return sql.ErrNoRows
//		}
//		log.Printf("DATABASE: Client %d updated successfully by agent %d\n", clientID, agentUserID)
//		return nil
//	}
func handleGetSalesPerformance(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}

	// Get number of months from query param, default to 6 or 12
	monthsStr := r.URL.Query().Get("months")
	months, err := strconv.Atoi(monthsStr)
	if err != nil || months <= 0 {
		months = 12 // Default to last 12 months
	}

	salesData, err := getMonthlyPolicyCount(agentUserID, months)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to retrieve sales performance data")
		return
	}

	respondJSON(w, http.StatusOK, salesData)
}

// func deleteClient(clientID int64, agentUserID int64) error {
// 	stmt, err := db.Prepare("DELETE FROM clients WHERE id = ? AND agent_user_id = ?")
// 	if err != nil {
// 		return fmt.Errorf("failed to prepare delete client statement: %w", err)
// 	}
// 	defer stmt.Close()
// 	res, err := stmt.Exec(clientID, agentUserID)
// 	if err != nil {
// 		return fmt.Errorf("failed to execute delete client: %w", err)
// 	}
// 	rowsAffected, err := res.RowsAffected()
// 	if err != nil {
// 		return fmt.Errorf("failed to get rows affected: %w", err)
// 	}
// 	if rowsAffected == 0 {
// 		return sql.ErrNoRows
// 	}
// 	log.Printf("DATABASE: Client %d deleted successfully by agent %d\n", clientID, agentUserID)
// 	return nil
// }

func getProducts(userID int64, categoryFilter, insurerFilter, searchTerm string) ([]AgentInsurerRelation, error) {
	query := `SELECT id, name, category, insurer_name, product_id, description, status, features, eligibility, term, exclusions, room_rent, premium_indication, insurer_logo_url, brochure_url, wording_url, claim_form_url, upfront_commission_percentage, trail_commission_percentage, created_at, updated_at FROM agent_insurer_relations where agent_user_id=?`
	args := []interface{}{userID}
	if categoryFilter != "" && categoryFilter != "All Categories" {
		query += " AND category = ?"
		args = append(args, categoryFilter)
	}
	if insurerFilter != "" && insurerFilter != "All Insurers" {
		query += " AND insurer_name = ?"
		args = append(args, insurerFilter)
	}
	if searchTerm != "" {
		query += " AND (name LIKE ? OR insurer_name LIKE ? OR description LIKE ?)"
		term := "%" + searchTerm + "%"
		args = append(args, term, term, term)
	}
	query += " ORDER BY category, name"
	rows, err := db.Query(query, args...)
	if err != nil {
		log.Printf("ERROR: Failed to query products: %v\n", err)
		return nil, fmt.Errorf("database query error")
	}
	defer rows.Close()
	products := []AgentInsurerRelation{}
	for rows.Next() {
		var p AgentInsurerRelation
		if err := rows.Scan(&p.ID, &p.Name, &p.Category, &p.InsurerName, &p.ProductID, &p.Description, &p.Status, &p.Features, &p.Eligibility, &p.Term, &p.Exclusions, &p.RoomRent, &p.PremiumIndication, &p.InsurerLogoURL, &p.BrochureURL, &p.WordingURL, &p.ClaimFormURL, &p.UpfrontCommissionPercentage, &p.TrailCommissionPercentage, &p.CreatedAt, &p.UpdatedAt); err != nil {
			log.Printf("ERROR: Failed to scan product row: %v\n", err)
			continue
		}
		products = append(products, p)
	}
	if err := rows.Err(); err != nil {
		log.Printf("ERROR: Error iterating product rows: %v\n", err)
		return nil, fmt.Errorf("database iteration error")
	}
	log.Printf("DATABASE: Found %d products.\n", len(products))
	return products, nil
}

func getProductByID(productID string) (*Product, error) {
	row := db.QueryRow(`SELECT id, name, category, insurer, description, status, features, eligibility, term, exclusions, room_rent, premium_indication, insurer_logo_url, brochure_url, wording_url, claim_form_url, upfront_commission_percentage, trail_commission_percentage, created_at, updated_at FROM products WHERE id = ?`, productID)
	p := &Product{}
	err := row.Scan(&p.ID, &p.Name, &p.Category, &p.Insurer, &p.Description, &p.Status, &p.Features, &p.Eligibility, &p.Term, &p.Exclusions, &p.RoomRent, &p.PremiumIndication, &p.InsurerLogoURL, &p.BrochureURL, &p.WordingURL, &p.ClaimFormURL, &p.UpfrontCommissionPercentage, &p.TrailCommissionPercentage, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("ERROR: Failed to scan product row: %v\n", err)
		} else {
			log.Printf("DATABASE: Product %s not found\n", productID)
		}
		return nil, err
	}
	return p, nil
}
func handleGetAgentFullClientData(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}

	log.Printf("API: Fetching full data for all clients of agent %d", agentUserID)

	// 1. Get all client IDs for the agent
	clientIDs := []int64{}
	rows, err := db.Query("SELECT id FROM clients WHERE agent_user_id = ? ORDER BY name ASC", agentUserID)
	if err != nil {
		log.Printf("ERROR: Failed to query client IDs for agent %d: %v", agentUserID, err)
		respondError(w, http.StatusInternalServerError, "Failed to retrieve client list")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			log.Printf("ERROR: Failed to scan client ID for agent %d: %v", agentUserID, err)
			// Continue processing other clients
			continue
		}
		clientIDs = append(clientIDs, id)
	}
	if err = rows.Err(); err != nil {
		log.Printf("ERROR: Row iteration error fetching client IDs for agent %d: %v", agentUserID, err)
		respondError(w, http.StatusInternalServerError, "Error reading client list")
		return
	}

	// 2. For each client ID, fetch all related data
	// WARNING: This is an N+1 query pattern and can be inefficient for many clients.
	// Consider optimizing with JOINs or fewer queries in production.
	allClientData := []ClientFullData{}
	for _, clientID := range clientIDs {
		client, err := getClientByID(clientID, agentUserID)
		if err != nil {
			log.Printf("WARN: Skipping client %d for agent %d due to error: %v", clientID, agentUserID, err)
			continue
		}

		policies, err := getPoliciesByClientID(clientID, agentUserID)
		if err != nil {
			log.Printf("WARN: Failed fetching policies for client %d: %v", clientID, err)
			policies = []Policy{}
		}

		comms, err := getCommunicationsByClientID(clientID, agentUserID)
		if err != nil {
			log.Printf("WARN: Failed fetching communications for client %d: %v", clientID, err)
			comms = []Communication{}
		}

		tasks, err := getAllClientTasks(clientID, agentUserID) // Use function that gets all tasks
		if err != nil {
			log.Printf("WARN: Failed fetching tasks for client %d: %v", clientID, err)
			tasks = []Task{}
		}

		docs, err := getDocumentsByClientID(clientID, agentUserID)
		if err != nil {
			log.Printf("WARN: Failed fetching documents for client %d: %v", clientID, err)
			docs = []Document{}
		}

		fullData := ClientFullData{
			Client:         *client,
			Policies:       policies,
			Communications: comms,
			Tasks:          tasks,
			Documents:      docs,
		}
		allClientData = append(allClientData, fullData)
	}

	log.Printf("API: Successfully assembled full data for %d clients for agent %d", len(allClientData), agentUserID)
	respondJSON(w, http.StatusOK, allClientData)
}

func getAgentInsurerPOCs(agentUserID int64) ([]AgentInsurerPOC, error) {
	log.Printf("DATABASE: Getting insurer POCs for agent %d\n", agentUserID)
	rows, err := db.Query(`SELECT id, agent_user_id, insurer_name, poc_email
                       FROM agent_insurer_pocs WHERE agent_user_id = ? ORDER BY insurer_name ASC`, agentUserID)
	if err != nil {
		log.Printf("ERROR: Query agent POCs failed: %v", err)
		return nil, err
	}
	defer rows.Close()

	pocs := []AgentInsurerPOC{}
	for rows.Next() {
		var poc AgentInsurerPOC
		if err := rows.Scan(&poc.ID, &poc.AgentUserID, &poc.InsurerName, &poc.PocEmail); err != nil {
			log.Printf("ERROR: Scan agent POC row failed: %v", err)
			continue
		}
		pocs = append(pocs, poc)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return pocs, nil
}

// Replaces all existing POCs for the agent with the provided list
func setAgentInsurerPOCs(agentUserID int64, pocs []AgentInsurerPOC) error {
	log.Printf("DATABASE: Setting insurer POCs for agent %d (count: %d)\n", agentUserID, len(pocs))
	// Use a transaction
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback() // Rollback if anything fails

	// 1. Delete existing POCs for the agent
	_, err = tx.Exec("DELETE FROM agent_insurer_pocs WHERE agent_user_id = ?", agentUserID)
	if err != nil {
		return fmt.Errorf("failed to delete existing POCs: %w", err)
	}

	// 2. Insert new POCs (limit to 6 on backend as well, though frontend should enforce)
	stmt, err := tx.Prepare("INSERT INTO agent_insurer_pocs (agent_user_id, insurer_name, poc_email) VALUES (?, ?, ?)")
	if err != nil {
		return fmt.Errorf("failed to prepare insert POC: %w", err)
	}
	defer stmt.Close()

	insertCount := 0
	for i, poc := range pocs {
		if i >= 6 { // Enforce limit
			log.Printf("WARN: Attempted to save more than 6 insurer POCs for agent %d. Truncating.", agentUserID)
			break
		}
		if poc.InsurerName == "" || poc.PocEmail == "" { // Basic validation
			log.Printf("WARN: Skipping POC entry with empty insurer or email for agent %d.", agentUserID)
			continue
		}
		_, err = stmt.Exec(agentUserID, poc.InsurerName, poc.PocEmail)
		if err != nil {
			// Check for unique constraint violation
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				log.Printf("WARN: Duplicate insurer name '%s' skipped for agent %d.", poc.InsurerName, agentUserID)
				continue // Skip duplicate instead of failing transaction
			}
			return fmt.Errorf("failed to insert POC for insurer '%s': %w", poc.InsurerName, err)
		}
		insertCount++
	}

	// Commit transaction
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	log.Printf("DATABASE: Successfully set %d insurer POCs for agent %d\n", insertCount, agentUserID)
	return nil
}
func getAgentInsurerPOCByInsurer(agentUserID int64, insurerName string) (*AgentInsurerPOC, error) {
	log.Printf("DATABASE: Getting POC for agent %d, insurer '%s'\n", agentUserID, insurerName)
	row := db.QueryRow(`SELECT id, agent_user_id, insurer_name, poc_email
                       FROM agent_insurer_pocs
                       WHERE agent_user_id = ? AND LOWER(insurer_name) = LOWER(?)`, // Case-insensitive match
		agentUserID, insurerName)
	poc := &AgentInsurerPOC{}
	err := row.Scan(&poc.ID, &poc.AgentUserID, &poc.InsurerName, &poc.PocEmail)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, sql.ErrNoRows
		}
		log.Printf("ERROR: Failed to scan agent POC row for insurer '%s': %v\n", insurerName, err)
		return nil, err
	}
	return poc, nil
}

func getUpcomingRenewals(agentUserID int64, days int) ([]RenewalPolicyView, error) {
	log.Printf("DATABASE: Fetching renewals for agent %d (next %d days)\n", agentUserID, days)
	now := time.Now()
	startDate := now.Format("2006-01-02")                   // Today
	endDate := now.AddDate(0, 0, days).Format("2006-01-02") // X days from now

	query := `SELECT
                p.id, p.client_id, p.agent_user_id, p.product_id, p.policy_number, p.insurer,
                p.premium, p.sum_insured, p.start_date, p.end_date, p.status, p.policy_doc_url,
                p.upfront_commission_amount, p.created_at, p.updated_at,
                c.name as client_name
              FROM policies p
              JOIN clients c ON p.client_id = c.id
              WHERE p.agent_user_id = ? AND p.status = 'Active' AND p.end_date >= ? AND p.end_date < ?
              ORDER BY p.end_date ASC`

	rows, err := db.Query(query, agentUserID, startDate, endDate)
	if err != nil {
		log.Printf("ERROR: Query upcoming renewals failed: %v", err)
		return nil, err
	}
	defer rows.Close()

	var renewals []RenewalPolicyView
	for rows.Next() {
		var r RenewalPolicyView
		if err := rows.Scan(
			&r.ID, &r.ClientID, &r.AgentUserID, &r.ProductID, &r.PolicyNumber, &r.Insurer,
			&r.Premium, &r.SumInsured, &r.StartDate, &r.EndDate, &r.Status, &r.PolicyDocURL,
			&r.UpfrontCommissionAmount, &r.CreatedAt, &r.UpdatedAt, &r.ClientName,
		); err != nil {
			log.Printf("ERROR: Scan renewal row failed: %v", err)
			continue
		}
		renewals = append(renewals, r)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return renewals, nil
}

// NEW: DB Function for All Agent Tasks (with filters/pagination)
func getAllAgentTasks(agentUserID int64, statusFilter string, page, pageSize int) ([]Task, int, error) {
	log.Printf("DATABASE: Fetching all tasks for agent %d (Status: %s, Page: %d, Size: %d)\n", agentUserID, statusFilter, page, pageSize)
	offset := (page - 1) * pageSize

	// Base query
	baseQuery := " FROM tasks WHERE agent_user_id = ? "
	countQuery := "SELECT COUNT(*) " + baseQuery
	dataQuery := `SELECT id, client_id, agent_user_id, description, due_date, is_urgent, is_completed, created_at, completed_at ` + baseQuery

	args := []interface{}{agentUserID}

	// Apply status filter
	if statusFilter == "pending" {
		dataQuery += " AND is_completed = 0"
		countQuery += " AND is_completed = 0"
	} else if statusFilter == "completed" {
		dataQuery += " AND is_completed = 1"
		countQuery += " AND is_completed = 1"
	}
	// Add other filters like date range if needed

	// Get total count for pagination
	var totalItems int
	err := db.QueryRow(countQuery, args...).Scan(&totalItems)
	if err != nil {
		log.Printf("ERROR: Count all tasks failed: %v", err)
		return nil, 0, err
	}
	print("Data Query: ", dataQuery)

	// Add ordering and pagination to data query
	dataQuery += " ORDER BY is_completed ASC, is_urgent DESC, ISNULL(due_date) ASC, due_date ASC, created_at DESC LIMIT ? OFFSET ?"
	args = append(args, pageSize, offset)

	// Fetch data
	rows, err := db.Query(dataQuery, args...)
	if err != nil {
		log.Printf("ERROR: Query all tasks failed: %v", err)
		return nil, 0, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.ClientID, &t.AgentUserID, &t.Description, &t.DueDate, &t.IsUrgent, &t.IsCompleted, &t.CreatedAt, &t.CompletedAt); err != nil {
			log.Printf("ERROR: Scan all tasks row failed: %v", err)
			continue
		}
		tasks = append(tasks, t)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	return tasks, totalItems, nil
}

// NEW: DB Function for Full Activity Log (with pagination)
func getFullActivityLog(agentUserID int64, page, pageSize int) ([]ActivityLog, int, error) {
	log.Printf("DATABASE: Fetching full activity log for agent %d (Page: %d, Size: %d)\n", agentUserID, page, pageSize)
	offset := (page - 1) * pageSize

	countQuery := "SELECT COUNT(*) FROM activity_log WHERE agent_user_id = ?"
	dataQuery := `SELECT id, agent_user_id, timestamp, activity_type, description, related_id
                  FROM activity_log WHERE agent_user_id = ?
                  ORDER BY timestamp DESC LIMIT ? OFFSET ?`
	args := []interface{}{agentUserID}

	// Get total count
	var totalItems int
	err := db.QueryRow(countQuery, args...).Scan(&totalItems)
	if err != nil {
		log.Printf("ERROR: Count activity log failed: %v", err)
		return nil, 0, err
	}

	// Fetch data
	pagedArgs := append(args, pageSize, offset)
	rows, err := db.Query(dataQuery, pagedArgs...)
	if err != nil {
		log.Printf("ERROR: Query full activity log failed: %v", err)
		return nil, 0, err
	}
	defer rows.Close()

	var activities []ActivityLog
	for rows.Next() {
		var a ActivityLog
		var related sql.NullString
		if err := rows.Scan(&a.ID, &a.AgentUserID, &a.Timestamp, &a.ActivityType, &a.Description, &related); err != nil {
			log.Printf("ERROR: Scan full activity log row failed: %v", err)
			continue
		}
		if related.Valid {
			a.RelatedID = related.String
		}
		activities = append(activities, a)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	return activities, totalItems, nil
}

func createProduct(product Product) error {
	stmt, err := db.Prepare(`INSERT INTO products (id, name, category, insurer, description, status, features, eligibility, term, exclusions, room_rent, premium_indication, insurer_logo_url, brochure_url, wording_url, claim_form_url, upfront_commission_percentage, trail_commission_percentage, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("failed to prepare insert product: %w", err)
	}
	defer stmt.Close()
	_, err = stmt.Exec(product.ID, product.Name, product.Category, product.Insurer, product.Description, product.Status, product.Features, product.Eligibility, product.Term, product.Exclusions, product.RoomRent, product.PremiumIndication, product.InsurerLogoURL, product.BrochureURL, product.WordingURL, product.ClaimFormURL, product.UpfrontCommissionPercentage, product.TrailCommissionPercentage, time.Now())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: products.id") {
			return fmt.Errorf("product ID '%s' already exists", product.ID)
		}
		return fmt.Errorf("failed to execute insert product: %w", err)
	}
	log.Printf("DATABASE: Product created with ID: %s\n", product.ID)
	return nil
}

func getPoliciesByClientID(clientID int64, agentUserID int64) ([]Policy, error) {
	rows, err := db.Query(`SELECT id, client_id, agent_user_id, product_id, policy_number, insurer, premium, sum_insured, start_date, end_date, status, policy_doc_url, upfront_commission_amount, created_at, updated_at FROM policies WHERE client_id = ? AND agent_user_id = ? ORDER BY end_date DESC`, clientID, agentUserID)
	if err != nil {
		log.Printf("ERROR: Query policies failed: %v", err)
		return nil, err
	}
	defer rows.Close()
	var policies []Policy
	for rows.Next() {
		var p Policy
		if err := rows.Scan(&p.ID, &p.ClientID, &p.AgentUserID, &p.ProductID, &p.PolicyNumber, &p.Insurer, &p.Premium, &p.SumInsured, &p.StartDate, &p.EndDate, &p.Status, &p.PolicyDocURL, &p.UpfrontCommissionAmount, &p.CreatedAt, &p.UpdatedAt); err != nil {
			log.Printf("ERROR: Scan policy row failed: %v", err)
			continue
		}
		policies = append(policies, p)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return policies, nil
}

// func createPolicy(policy Policy) (string, error) {
// 	if policy.ID == "" {
// 		policy.ID = "POL-" + generateSimpleID(8)
// 	}
// 	policy.CreatedAt = time.Now()
// 	var commissionAmount float64 = 0
// 	var commissionValid bool = false
// 	log.Printf("DAkar  : Policy created wit: %s\n", policy.ProductID.String)

// 	if policy.ProductID.Valid {
// 		product, err := getProductByID(policy.ProductID.String)
// 		log.Printf("DATABASE: Policy created wit: %s\n", policy.ProductID.String)

// 		if err != nil {
// 			log.Printf("WARN: Could not fetch product %s to calculate commission: %v", policy.ProductID.String, err)
// 		} else if product != nil && product.UpfrontCommissionPercentage.Valid {
// 			commissionAmount = policy.Premium * (product.UpfrontCommissionPercentage.Float64 / 100.0)
// 			commissionAmount = math.Round(commissionAmount*100) / 100
// 			commissionValid = true
// 			log.Printf("DATABASE: Calculated upfront commission for policy %s: %.2f", policy.ID, commissionAmount)
// 		}
// 	}
// 	policy.UpfrontCommissionAmount = sql.NullFloat64{Float64: commissionAmount, Valid: commissionValid}

// 	stmt, err := db.Prepare(`INSERT INTO policies (id, client_id, agent_user_id, product_id, policy_number, insurer, premium, sum_insured, start_date, end_date, status, policy_doc_url, upfront_commission_amount, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
// 	if err != nil {
// 		return "", fmt.Errorf("failed to prepare insert policy: %w", err)
// 	}
// 	defer stmt.Close()
// 	_, err = stmt.Exec(policy.ID, policy.ClientID, policy.AgentUserID, policy.ProductID, policy.PolicyNumber, policy.Insurer, policy.Premium, policy.SumInsured, policy.StartDate, policy.EndDate, policy.Status, policy.PolicyDocURL, policy.UpfrontCommissionAmount, policy.CreatedAt)
// 	if err != nil {
// 		return "", fmt.Errorf("failed to execute insert policy: %w", err)
// 	}
// 	log.Printf("DATABASE: Policy created with ID: %s\n", policy.ID)
// 	return policy.ID, nil
// }

func getCommunicationsByClientID(clientID int64, agentUserID int64) ([]Communication, error) {
	rows, err := db.Query(`SELECT id, client_id, agent_user_id, type, timestamp, summary, created_at FROM communications WHERE client_id = ? AND agent_user_id = ? ORDER BY timestamp DESC`, clientID, agentUserID)
	if err != nil {
		log.Printf("ERROR: Query communications failed: %v", err)
		return nil, err
	}
	defer rows.Close()
	var comms []Communication
	for rows.Next() {
		var c Communication
		if err := rows.Scan(&c.ID, &c.ClientID, &c.AgentUserID, &c.Type, &c.Timestamp, &c.Summary, &c.CreatedAt); err != nil {
			log.Printf("ERROR: Scan communication row failed: %v", err)
			continue
		}
		comms = append(comms, c)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return comms, nil
}

func createCommunication(comm Communication) (int64, error) {
	stmt, err := db.Prepare(`INSERT INTO communications (client_id, agent_user_id, type, timestamp, summary) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("failed to prepare insert communication: %w", err)
	}
	defer stmt.Close()
	res, err := stmt.Exec(comm.ClientID, comm.AgentUserID, comm.Type, comm.Timestamp, comm.Summary)
	if err != nil {
		return 0, fmt.Errorf("failed to execute insert communication: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get last insert ID: %w", err)
	}
	log.Printf("DATABASE: Communication log created with ID: %d\n", id)
	return id, nil
}

func getTasksByClientID(clientID int64, agentUserID int64) ([]Task, error) {
	rows, err := db.Query(`SELECT id, client_id, agent_user_id, description, due_date, is_urgent, is_completed, created_at, completed_at FROM tasks WHERE client_id = ? AND agent_user_id = ? AND is_completed = 0 ORDER BY is_urgent DESC, due_date ASC, created_at DESC`, clientID, agentUserID)
	if err != nil {
		log.Printf("ERROR: Query tasks failed: %v", err)
		return nil, err
	}
	defer rows.Close()
	var tasks []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.ClientID, &t.AgentUserID, &t.Description, &t.DueDate, &t.IsUrgent, &t.IsCompleted, &t.CreatedAt, &t.CompletedAt); err != nil {
			log.Printf("ERROR: Scan task row failed: %v", err)
			continue
		}
		tasks = append(tasks, t)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return tasks, nil
}

func createTask(task Task) (int64, error) {
	stmt, err := db.Prepare(`INSERT INTO tasks (client_id, agent_user_id, description, due_date, is_urgent, is_completed) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("failed to prepare insert task: %w", err)
	}
	defer stmt.Close()
	res, err := stmt.Exec(task.ClientID, task.AgentUserID, task.Description, task.DueDate, task.IsUrgent, false)
	if err != nil {
		return 0, fmt.Errorf("failed to execute insert task: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get last insert ID: %w", err)
	}
	log.Printf("DATABASE: Task created with ID: %d\n", id)
	return id, nil
}

func getDocumentsByClientID(clientID int64, agentUserID int64) ([]Document, error) {
	rows, err := db.Query(`SELECT id, client_id, agent_user_id, title, document_type, file_url, uploaded_at FROM documents WHERE client_id = ? AND agent_user_id = ? ORDER BY uploaded_at DESC`, clientID, agentUserID)
	if err != nil {
		log.Printf("ERROR: Query documents failed: %v", err)
		return nil, err
	}
	defer rows.Close()
	var docs []Document
	for rows.Next() {
		var d Document
		if err := rows.Scan(&d.ID, &d.ClientID, &d.AgentUserID, &d.Title, &d.DocumentType, &d.FileURL, &d.UploadedAt); err != nil {
			log.Printf("ERROR: Scan document row failed: %v", err)
			continue
		}
		docs = append(docs, d)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return docs, nil
}

func createDocument(doc Document) (int64, error) {
	stmt, err := db.Prepare(`INSERT INTO documents (client_id, agent_user_id, title, document_type, file_url) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("failed to prepare insert document: %w", err)
	}
	defer stmt.Close()
	res, err := stmt.Exec(doc.ClientID, doc.AgentUserID, doc.Title, doc.DocumentType, doc.FileURL)
	if err != nil {
		return 0, fmt.Errorf("failed to execute insert document: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get last insert ID: %w", err)
	}
	log.Printf("DATABASE: Document record created with ID: %d\n", id)
	return id, nil
}

func getMarketingCampaigns(agentUserID int64) ([]MarketingCampaign, error) {
	rows, err := db.Query(`SELECT id, agent_user_id, name, status, target_segment_name, sent_at, stats_opens, stats_clicks, stats_leads, created_at FROM marketing_campaigns ORDER BY created_at DESC`)
	log.Print("Errpr %s", agentUserID)
	if err != nil {
		log.Printf("ERROR: Query campaigns failed: %v", err)
		return nil, err
	}
	defer rows.Close()
	var campaigns []MarketingCampaign
	for rows.Next() {
		var c MarketingCampaign
		if err := rows.Scan(&c.ID, &c.AgentUserID, &c.Name, &c.Status, &c.TargetSegmentName, &c.SentAt, &c.StatsOpens, &c.StatsClicks, &c.StatsLeads, &c.CreatedAt); err != nil {
			log.Printf("ERROR: Scan campaign row failed: %v", err)
			continue
		}
		campaigns = append(campaigns, c)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return campaigns, nil
}

func createMarketingCampaign(campaign MarketingCampaign) (int64, error) {
	stmt, err := db.Prepare(`INSERT INTO marketing_campaigns (agent_user_id, name, status, target_segment_name, created_at) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("failed to prepare insert campaign: %w", err)
	}
	defer stmt.Close()
	res, err := stmt.Exec(campaign.AgentUserID, campaign.Name, campaign.Status, campaign.TargetSegmentName, time.Now())
	if err != nil {
		return 0, fmt.Errorf("failed to execute insert campaign: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get last insert ID: %w", err)
	}
	log.Printf("DATABASE: Campaign created with ID: %d\n", id)
	return id, nil
}

func getMarketingTemplates() ([]MarketingTemplate, error) {
	rows, err := db.Query(`SELECT id, name, type, category, preview_text, created_at FROM marketing_templates ORDER BY category, name`)
	if err != nil {
		log.Printf("ERROR: Query templates failed: %v", err)
		return nil, err
	}
	defer rows.Close()
	var templates []MarketingTemplate
	for rows.Next() {
		var t MarketingTemplate
		if err := rows.Scan(&t.ID, &t.Name, &t.Type, &t.Category, &t.PreviewText, &t.CreatedAt); err != nil {
			log.Printf("ERROR: Scan template row failed: %v", err)
			continue
		}
		templates = append(templates, t)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return templates, nil
}

func getMarketingContent() ([]MarketingContent, error) {
	rows, err := db.Query(`SELECT id, title, content_type, description, gcs_url, thumbnail_url, created_at FROM marketing_content ORDER BY created_at DESC`)
	if err != nil {
		log.Printf("ERROR: Query content failed: %v", err)
		return nil, err
	}
	defer rows.Close()
	var contents []MarketingContent
	for rows.Next() {
		var c MarketingContent
		if err := rows.Scan(&c.ID, &c.Title, &c.ContentType, &c.Description, &c.GCSURL, &c.ThumbnailURL, &c.CreatedAt); err != nil {
			log.Printf("ERROR: Scan content row failed: %v", err)
			continue
		}
		contents = append(contents, c)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return contents, nil
}

func getClientSegments(agentUserID int64) ([]ClientSegment, error) {
	rows, err := db.Query(`SELECT id, agent_user_id, name, criteria, client_count, created_at FROM client_segments WHERE agent_user_id = ? ORDER BY name ASC`, agentUserID)
	if err != nil {
		log.Printf("ERROR: Query segments failed: %v", err)
		return nil, err
	}
	defer rows.Close()
	var segments []ClientSegment
	for rows.Next() {
		var s ClientSegment
		if err := rows.Scan(&s.ID, &s.AgentUserID, &s.Name, &s.Criteria, &s.ClientCount, &s.CreatedAt); err != nil {
			log.Printf("ERROR: Scan segment row failed: %v", err)
			continue
		}
		segments = append(segments, s)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return segments, nil
}

func createClientSegment(segment ClientSegment) (int64, error) {
	stmt, err := db.Prepare(`INSERT INTO client_segments (agent_user_id, name, criteria, client_count) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("failed to prepare insert segment: %w", err)
	}
	defer stmt.Close()
	res, err := stmt.Exec(segment.AgentUserID, segment.Name, segment.Criteria, segment.ClientCount)
	if err != nil {
		return 0, fmt.Errorf("failed to execute insert segment: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get last insert ID: %w", err)
	}
	log.Printf("DATABASE: Client Segment created with ID: %d\n", id)
	return id, nil
}

type EmailConfig struct {
	SMTPServer string
	SMTPPort   string
	Username   string
	Password   string
	EmailFrom  string
}

// --- Email (Mocked Functions) ---

// sendEmail sends an email using the provided configuration.
func sendEmail(to []string, subject, body string) error {
	// func sendEmail(to, subject, body string) error {

	// Construct the message.
	msg := []byte(strings.Join([]string{
		"From: " + "clientwise.co@gmail.com",
		"To: " + strings.Join(to, ","), // Join multiple recipients with commas
		"Subject: " + subject,
		"MIME-version: 1.0",                          // Add MIME version header
		"Content-Type: text/html; charset=\"UTF-8\"", // Specify HTML content type
		"", // Empty line before the body
		body,
	}, "\r\n"))

	config := EmailConfig{
		SMTPServer: "smtp.gmail.com",        // Replace with your SMTP server
		SMTPPort:   "587",                   // Replace with your SMTP port (e.g., 587 for TLS, 465 for SSL)
		Username:   "admin@goclientwise.in", // Replace with your email address
		Password:   "qoyh brmf joat dfge",   // Replace with your email password or an app password
		EmailFrom:  "admin@goclientwise.in", // Replace with the sender email address
	}

	// Set up authentication.
	auth := smtp.PlainAuth("", config.Username, config.Password, config.SMTPServer)

	// Construct the server address.
	addr := config.SMTPServer + ":" + config.SMTPPort

	// Send the email.
	err := smtp.SendMail(addr, auth, config.EmailFrom, to, msg)
	if err != nil {
		log.Printf("Error sending email: %v", err) // Log the error
		return err                                 // Return the error for the caller to handle
	}

	log.Println("Email sent successfully!")
	return nil
}
func sendVerificationEmail(email, token string) error {
	subject := "Verify Your ClientWise Account"
	verificationLink := config.VerificationURL + token
	body := fmt.Sprintf(`<h2>Welcome!</h2><p>Click to verify: <a href="%s">Verify Email</a></p>`, verificationLink)
	return sendEmail([]string{email}, subject, body)
}
func sendWelcomeEmail(email string) error {
	subject := "Welcome to ClientWise!"
	body := `<h2>Welcome Aboard!</h2><p>Your account is ready.</p>`
	return sendEmail([]string{email}, subject, body)
}
func sendResetEmail(email, token string) error {
	subject := "Reset Your ClientWise Password"
	resetLink := config.ResetURL + token
	body := fmt.Sprintf(`<h2>Password Reset</h2><p>Click to reset (1hr expiry): <a href="%s">Reset Password</a></p>`, resetLink)
	return sendEmail([]string{email}, subject, body)
}
func sendLoginNotification(email string) error {
	subject := "Successful Login to ClientWise"
	body := fmt.Sprintf(`<h2>Login Notification</h2><p>Your account (%s) was logged into.</p>`, email)
	return sendEmail([]string{email}, subject, body)
}

// --- Authentication Helpers ---
func hashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), 14)
	return string(bytes), err
}
func checkPasswordHash(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}
func generateToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
func generateSimpleID(length int) string {
	b := make([]byte, length)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// --- Context Helpers ---
type contextKey string

const userIDKey contextKey = "userID"
const userTypeKey contextKey = "userType"

func getUserIDFromContext(ctx context.Context) (int64, bool) {
	userID, ok := ctx.Value(userIDKey).(int64)
	return userID, ok
}
func getUserTypeFromContext(ctx context.Context) (string, bool) {
	userType, ok := ctx.Value(userTypeKey).(string)
	return userType, ok
}

// --- HTTP Handlers ---
func respondJSON(w http.ResponseWriter, status int, payload interface{}) {
	response, err := json.Marshal(payload)
	if err != nil {
		log.Printf("ERROR: Marshal JSON: %v", err)
		http.Error(w, `{"error":"Internal Server Error"}`, 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(response)
}
func respondError(w http.ResponseWriter, status int, message string) {
	log.Printf("RESPONSE ERROR: Status %d, Message: %s", status, message)
	respondJSON(w, status, map[string]string{"error": message})
}

func handleSignup(w http.ResponseWriter, r *http.Request) {
	var creds struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		UserType string `json:"userType"`
	}
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}
	if creds.Email == "" || creds.Password == "" || (creds.UserType != "agent" && creds.UserType != "agency") {
		respondError(w, http.StatusBadRequest, "Missing required fields or invalid user type")
		return
	}
	_, err := getUserByEmail(creds.Email)
	if err == nil {
		respondError(w, http.StatusConflict, "Email address already registered")
		return
	}
	if err != sql.ErrNoRows {
		log.Printf("ERROR: DB check user: %v", err)
		respondError(w, http.StatusInternalServerError, "Database error")
		return
	}
	hashedPassword, err := hashPassword(creds.Password)
	if err != nil {
		log.Printf("ERROR: Hash password: %v", err)
		respondError(w, http.StatusInternalServerError, "Failed to process password")
		return
	}
	newUser := User{Email: creds.Email, PasswordHash: hashedPassword, UserType: creds.UserType, IsVerified: false}
	userID, err := createUser(newUser)
	if err != nil {
		log.Printf("ERROR: Create user: %v", err)
		respondError(w, http.StatusInternalServerError, "Failed to create user")
		return
	}
	token, err := generateToken(32)
	if err != nil {
		log.Printf("ERROR: Generate verification token: %v", err)
		respondError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}
	err = storeToken(userID, token, "verification", 24*time.Hour)
	if err != nil {
		log.Printf("ERROR: Store verification token: %v", err)
		respondError(w, http.StatusInternalServerError, "Failed to store token")
		return
	}
	go sendVerificationEmail(creds.Email, token)
	log.Printf("SIGNUP: User %s registered (ID: %d). Verification email logged.", creds.Email, userID)
	respondJSON(w, http.StatusCreated, map[string]string{"message": "Signup successful! Please check your email/console log to verify your account."})
}
func handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		respondError(w, http.StatusBadRequest, "Verification token missing")
		return
	}
	userID, err := verifyToken(token, "verification")
	if err != nil {
		log.Printf("VERIFY: Invalid/expired token: %s", token)
		respondError(w, http.StatusBadRequest, "Invalid or expired verification link")
		return
	}
	err = markUserVerified(userID)
	if err != nil {
		log.Printf("ERROR: Mark user verified %d: %v", userID, err)
		respondError(w, http.StatusInternalServerError, "Failed to update verification status")
		return
	}
	err = deleteTokenByUserID(userID, "verification")
	if err != nil {
		log.Printf("WARN: Failed to delete verification token for user %d: %v", userID, err)
	}
	user, dbErr := getUserByEmail(fmt.Sprintf("user_%d@example.com", userID)) // Placeholder
	if dbErr == nil && user != nil {
		go sendWelcomeEmail(user.Email)
	} else {
		go sendWelcomeEmail(fmt.Sprintf("user_%d@example.com", userID))
	}
	log.Printf("VERIFY: User %d successfully verified.", userID)
	http.Redirect(w, r, config.CorsOrigin+"/login?verified=true", http.StatusFound)
}
func handleLogin(w http.ResponseWriter, r *http.Request) {
	var creds struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		UserType string `json:"userType"`
	}
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}
	if creds.Email == "" || creds.Password == "" || (creds.UserType != "agent" && creds.UserType != "agency") {
		respondError(w, http.StatusBadRequest, "Missing fields or invalid user type")
		return
	}
	user, err := getUserByEmail(creds.Email)
	if err != nil {
		if err == sql.ErrNoRows {
			respondError(w, http.StatusUnauthorized, "Invalid email or password")
			return
		}
		log.Printf("ERROR: DB get user: %v", err)
		respondError(w, http.StatusInternalServerError, "Database error")
		return
	}
	if !user.IsVerified {
		log.Printf("LOGIN: Unverified user: %s", creds.Email)
		respondError(w, http.StatusForbidden, "Account not verified. Please check your email.")
		return
	}
	if user.UserType != creds.UserType {
		log.Printf("LOGIN: Type mismatch for %s", creds.Email)
		respondError(w, http.StatusUnauthorized, "Login type mismatch")
		return
	}
	if !checkPasswordHash(creds.Password, user.PasswordHash) {
		log.Printf("LOGIN: Invalid password for %s", creds.Email)
		respondError(w, http.StatusUnauthorized, "Invalid email or password")
		return
	}
	expirationTime := time.Now().Add(time.Duration(config.JWTExpiryHours) * time.Hour)
	claims := &Claims{UserID: user.ID, UserType: user.UserType, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(expirationTime), IssuedAt: jwt.NewNumericDate(time.Now()), NotBefore: jwt.NewNumericDate(time.Now()), Issuer: "clientwise", Subject: fmt.Sprintf("%d", user.ID)}}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(jwtSecretKey)
	if err != nil {
		log.Printf("ERROR: Failed to sign JWT for user %d: %v", user.ID, err)
		respondError(w, http.StatusInternalServerError, "Could not generate login token")
		return
	}
	go sendLoginNotification(user.Email)
	log.Printf("LOGIN: Successful login for %s (ID: %d). JWT generated.", user.Email, user.ID)
	respondJSON(w, http.StatusOK, map[string]interface{}{"message": "Login successful", "userId": user.ID, "userType": user.UserType, "token": tokenString, "expiresAt": expirationTime.Unix()})
}

// --- UPDATED: Public Onboarding Handler ---
func handlePublicOnboarding(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// 1. Get Agent ID from query parameter
	agentIDStr := r.URL.Query().Get("agentId")
	agentID, err := strconv.ParseInt(agentIDStr, 10, 64)
	if err != nil || agentID <= 0 {
		respondError(w, http.StatusBadRequest, "Invalid or missing agent identifier in the link.")
		return
	}

	// TODO: Optional: Verify agent ID exists in the users table

	// 2. Decode Payload
	var payload OnboardingPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid form data submitted.")
		return
	}

	// 3. Validate Payload
	if payload.Name == "" || (payload.Email == "" && payload.Phone == "") {
		respondError(w, http.StatusBadRequest, "Your name and at least email or phone are required.")
		return
	}

	// 4. Create Client Struct
	newClient := Client{
		AgentUserID: agentID, Name: payload.Name,
		Email:         sql.NullString{String: payload.Email, Valid: payload.Email != ""},
		Phone:         sql.NullString{String: payload.Phone, Valid: payload.Phone != ""},
		Dob:           sql.NullString{String: payload.Dob, Valid: payload.Dob != ""},
		Address:       sql.NullString{String: payload.Address, Valid: payload.Address != ""},
		Status:        "Lead", // Default status
		Tags:          sql.NullString{String: payload.Tags, Valid: payload.Tags != ""},
		Income:        sql.NullFloat64{Float64: *payload.Income, Valid: payload.Income != nil},
		MaritalStatus: sql.NullString{String: payload.MaritalStatus, Valid: payload.MaritalStatus != ""},
		City:          sql.NullString{String: payload.City, Valid: payload.City != ""},
		JobProfile:    sql.NullString{String: payload.JobProfile, Valid: payload.JobProfile != ""},
		Dependents:    sql.NullInt64{Int64: *payload.Dependents, Valid: payload.Dependents != nil},
		Liability:     sql.NullFloat64{Float64: *payload.Liability, Valid: payload.Liability != nil},
		HousingType:   sql.NullString{String: payload.HousingType, Valid: payload.HousingType != ""},
		VehicleCount:  sql.NullInt64{Int64: *payload.VehicleCount, Valid: payload.VehicleCount != nil},
		VehicleType:   sql.NullString{String: payload.VehicleType, Valid: payload.VehicleType != ""},
		VehicleCost:   sql.NullFloat64{Float64: *payload.VehicleCost, Valid: payload.VehicleCost != nil},
	}

	// 5. Save to Database
	clientID, err := createClient(newClient)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			respondError(w, http.StatusConflict, "This email or phone number is already registered with this agent.")
			return
		}
		log.Printf("ERROR: Failed to create client from onboarding for agent %d: %v", agentID, err)
		respondError(w, http.StatusInternalServerError, "Failed to save details. Please try again later.")
		return
	}
	newClient.ID = clientID // Add ID for estimation step

	// 6. Log Activity (Optional)
	logActivity(agentID, "lead_onboarded", fmt.Sprintf("Client '%s' submitted onboarding form", newClient.Name), fmt.Sprintf("%d", clientID))

	// 7. Perform Coverage Estimation using the *just created* client data
	// We need the full Client struct, so we re-fetch it (alternatively, createClient could return the full struct)
	// For simplicity, let's assume newClient (with ID) has enough info, or ideally refetch
	// Refetching is safer if createClient doesn't return all fields or defaults are applied in DB
	fetchedClient, err := getClientByID(clientID, agentID) // Need to ensure this works without JWT context if called here, OR pass agentID
	var estimation *CoverageEstimation                     // Use pointer to handle potential errors gracefully

	if err != nil {
		log.Printf("WARN: Could not fetch client %d immediately after creation for estimation: %v", clientID, err)
		// Continue without estimation in case of error fetching the new client
	} else if fetchedClient != nil {
		calcEst := estimateCoverage(*fetchedClient)
		estimation = &calcEst // Assign calculated estimation
	}

	// 8. Respond Success (including estimation if calculated)
	log.Printf("ONBOARDING: Client %d created successfully for agent %d", clientID, agentID)
	respondJSON(w, http.StatusCreated, map[string]interface{}{
		"message":    "Thank you! Your details have been submitted successfully.",
		"estimation": estimation, // Include estimation in the response (will be null if calculation failed)
	})
}

func handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}
	if req.Email == "" {
		respondError(w, http.StatusBadRequest, "Email is required")
		return
	}
	user, err := getUserByEmail(req.Email)
	if err != nil && err != sql.ErrNoRows {
		log.Printf("ERROR: ForgotPassword DB error getting user %s: %v", req.Email, err)
	}
	if user != nil {
		token, err := generateToken(32)
		if err != nil {
			log.Printf("ERROR: Generate reset token for %s: %v", req.Email, err)
		} else {
			err = storeToken(user.ID, token, "reset", 1*time.Hour)
			if err != nil {
				log.Printf("ERROR: Store reset token for %s: %v", req.Email, err)
			} else {
				go sendResetEmail(user.Email, token)
			}
		}
	} else {
		log.Printf("FORGOT_PW: Request for non-existent email: %s", req.Email)
	}
	log.Printf("FORGOT_PW: Reset initiated for (if exists): %s", req.Email)
	respondJSON(w, http.StatusOK, map[string]string{"message": "If an account with that email exists, a password reset link has been sent (check console log)."})
}

// NEW: Agent Profile DB Functions
func getAgentProfile(userID int64) (*AgentProfile, error) {
	log.Printf("DATABASE: Getting agent profile for user %d\n", userID)
	row := db.QueryRow(`SELECT user_id, mobile, gender, postal_address, agency_name, pan, bank_name, bank_account_no, bank_ifsc
                       FROM agent_profiles WHERE user_id = ?`, userID)
	profile := &AgentProfile{}
	err := row.Scan(
		&profile.UserID, &profile.Mobile, &profile.Gender, &profile.PostalAddress, &profile.AgencyName,
		&profile.PAN, &profile.BankName, &profile.BankAccountNo, &profile.BankIFSC,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, sql.ErrNoRows
		} // Return specific error for not found
		log.Printf("ERROR: Failed to scan agent profile row for user %d: %v\n", userID, err)
		return nil, err
	}
	return profile, nil
}

func upsertAgentProfile(profile AgentProfile) error {
	log.Printf("DATABASE: Upserting agent profile for user %d\n", profile.UserID)
	// Using INSERT OR REPLACE - this replaces the entire row if user_id exists.
	// Alternatively, use INSERT ON CONFLICT UPDATE for more granular updates.
	stmt, err := db.Prepare(`INSERT INTO agent_profiles
        (user_id, mobile, gender, postal_address, agency_name, pan, bank_name, bank_account_no, bank_ifsc)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("failed to prepare upsert agent profile: %w", err)
	}
	defer stmt.Close()

	_, err = stmt.Exec(
		profile.UserID, profile.Mobile, profile.Gender, profile.PostalAddress, profile.AgencyName,
		profile.PAN, profile.BankName, profile.BankAccountNo, profile.BankIFSC,
	)
	if err != nil {
		// Check for specific errors like UNIQUE constraint on PAN if needed
		if strings.Contains(err.Error(), "UNIQUE constraint failed: agent_profiles.pan") {
			return fmt.Errorf("PAN number already exists for another user")
		}
		return fmt.Errorf("failed to execute upsert agent profile: %w", err)
	}
	log.Printf("DATABASE: Agent profile upserted successfully for user %d\n", profile.UserID)
	return nil
}

// NEW: Agent Goal DB Functions
func getAgentGoal(userID int64) (*AgentGoal, error) {
	log.Printf("DATABASE: Getting agent goals for user %d\n", userID)
	row := db.QueryRow(`SELECT user_id, target_income, target_period FROM agent_goals WHERE user_id = ?`, userID)
	goal := &AgentGoal{}
	err := row.Scan(&goal.UserID, &goal.TargetIncome, &goal.TargetPeriod)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, sql.ErrNoRows
		}
		log.Printf("ERROR: Failed to scan agent goal row for user %d: %v\n", userID, err)
		return nil, err
	}
	return goal, nil
}

func upsertAgentGoal(goal AgentGoal) error {
	log.Printf("DATABASE: Upserting agent goal for user %d\n", goal.UserID)
	stmt, err := db.Prepare(`INSERT INTO agent_goals (user_id, target_income, target_period) VALUES (?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("failed to prepare upsert agent goal: %w", err)
	}
	defer stmt.Close()
	_, err = stmt.Exec(goal.UserID, goal.TargetIncome, goal.TargetPeriod)
	if err != nil {
		return fmt.Errorf("failed to execute upsert agent goal: %w", err)
	}
	log.Printf("DATABASE: Agent goal upserted successfully for user %d\n", goal.UserID)
	return nil
}

func handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token       string `json:"token"`
		NewPassword string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}
	if req.Token == "" || req.NewPassword == "" {
		respondError(w, http.StatusBadRequest, "Token and new password required")
		return
	}
	userID, err := verifyToken(req.Token, "reset")
	if err != nil {
		log.Printf("RESET_PW: Invalid/expired token: %s", req.Token)
		respondError(w, http.StatusBadRequest, "Invalid or expired reset link")
		return
	}
	newPasswordHash, err := hashPassword(req.NewPassword)
	if err != nil {
		log.Printf("ERROR: Hash new password %d: %v", userID, err)
		respondError(w, http.StatusInternalServerError, "Failed to process password")
		return
	}
	err = updateUserPassword(userID, newPasswordHash)
	if err != nil {
		log.Printf("ERROR: Update password %d: %v", userID, err)
		respondError(w, http.StatusInternalServerError, "Failed to update password")
		return
	}
	err = deleteTokenByUserID(userID, "reset")
	if err != nil {
		log.Printf("WARN: Failed to delete reset token for user %d: %v", userID, err)
	}
	log.Printf("RESET_PW: Password reset successful for user %d", userID)
	respondJSON(w, http.StatusOK, map[string]string{"message": "Password reset successfully. You can now log in."})
}
func handleGetNotices(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	notices, err := getNotices(category)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to retrieve notices")
		return
	}
	respondJSON(w, http.StatusOK, notices)
}

//	func handleGetProducts(w http.ResponseWriter, r *http.Request) {
//		categoryFilter := r.URL.Query().Get("category")
//		insurerFilter := r.URL.Query().Get("insurer")
//		searchTerm := r.URL.Query().Get("search")
//		products, err := getProducts(categoryFilter, insurerFilter, searchTerm)
//		if err != nil {
//			respondError(w, http.StatusInternalServerError, "Failed to retrieve products")
//			return
//		}
//		respondJSON(w, http.StatusOK, products)
//	}
func handleGetProduct(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "productId")
	if id == "" {
		respondError(w, http.StatusBadRequest, "Product ID missing in URL path")
		return
	}
	product, err := getProductByID(id)
	if err != nil {
		if err == sql.ErrNoRows {
			respondError(w, http.StatusNotFound, "Product not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to retrieve product")
		return
	}
	respondJSON(w, http.StatusOK, product)
}
func handleCreateProduct(w http.ResponseWriter, r *http.Request) {
	var payload CreateProductPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request payload: "+err.Error())
		return
	}
	if payload.ID == "" || payload.Name == "" || payload.Category == "" || payload.Insurer == "" {
		respondError(w, http.StatusBadRequest, "Product ID, Name, Category, and Insurer are required")
		return
	}
	if payload.Features != nil && *payload.Features != "" {
		var featuresList []string
		if err := json.Unmarshal([]byte(*payload.Features), &featuresList); err != nil {
			respondError(w, http.StatusBadRequest, "Invalid JSON format for features field")
			return
		}
	}
	status := "Active"
	if payload.Status != "" {
		status = payload.Status
	}
	var upfrontComm sql.NullFloat64
	if payload.UpfrontCommissionPercentage != nil {
		upfrontComm = sql.NullFloat64{Float64: *payload.UpfrontCommissionPercentage, Valid: true}
	}
	var trailComm sql.NullFloat64
	if payload.TrailCommissionPercentage != nil {
		trailComm = sql.NullFloat64{Float64: *payload.TrailCommissionPercentage, Valid: true}
	}
	newProduct := Product{ID: payload.ID, Name: payload.Name, Category: payload.Category, Insurer: payload.Insurer, Description: sql.NullString{String: *payload.Description, Valid: payload.Description != nil}, Status: status, Features: sql.NullString{String: *payload.Features, Valid: payload.Features != nil}, Eligibility: sql.NullString{String: *payload.Eligibility, Valid: payload.Eligibility != nil}, Term: sql.NullString{String: *payload.Term, Valid: payload.Term != nil}, Exclusions: sql.NullString{String: *payload.Exclusions, Valid: payload.Exclusions != nil}, RoomRent: sql.NullString{String: *payload.RoomRent, Valid: payload.RoomRent != nil}, PremiumIndication: sql.NullString{String: *payload.PremiumIndication, Valid: payload.PremiumIndication != nil}, InsurerLogoURL: sql.NullString{String: *payload.InsurerLogoURL, Valid: payload.InsurerLogoURL != nil}, BrochureURL: sql.NullString{String: *payload.BrochureURL, Valid: payload.BrochureURL != nil}, WordingURL: sql.NullString{String: *payload.WordingURL, Valid: payload.WordingURL != nil}, ClaimFormURL: sql.NullString{String: *payload.ClaimFormURL, Valid: payload.ClaimFormURL != nil}, UpfrontCommissionPercentage: upfrontComm, TrailCommissionPercentage: trailComm, CreatedAt: time.Now()}
	err := createProduct(newProduct)
	if err != nil {
		log.Printf("ERROR: Failed to create product %s: %v", newProduct.ID, err)
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			respondError(w, http.StatusConflict, fmt.Sprintf("Product with ID '%s' already exists.", newProduct.ID))
		} else {
			respondError(w, http.StatusInternalServerError, "Failed to create product")
		}
		return
	}
	respondJSON(w, http.StatusCreated, newProduct)
}

//	func handleGetClients2(w http.ResponseWriter, r *http.Request) {
//		agentUserID, ok := getUserIDFromContext(r.Context())
//		if !ok {
//			respondError(w, http.StatusInternalServerError, "Could not get user ID from context")
//			return
//		}
//		statusFilter := r.URL.Query().Get("status")
//		searchTerm := r.URL.Query().Get("search")
//		limitStr := r.URL.Query().Get("limit")
//		offsetStr := r.URL.Query().Get("offset")
//		limit, _ := strconv.Atoi(limitStr)
//		offset, _ := strconv.Atoi(offsetStr)
//		if limit <= 0 || limit > 100 {
//			limit = 25
//		}
//		if offset < 0 {
//			offset = 0
//		}
//		clients, err := getClientsByAgentID(agentUserID, statusFilter, searchTerm, limit, offset)
//		if err != nil {
//			respondError(w, http.StatusInternalServerError, "Failed to retrieve clients")
//			return
//		}
//		respondJSON(w, http.StatusOK, clients)
//	}
func handleCreateClient(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Could not get user ID from context")
		return
	}
	var payload ClientPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}
	if payload.Name == "" {
		respondError(w, http.StatusBadRequest, "Client name is required")
		return
	} // Simplified validation

	newClient := Client{
		AgentUserID: agentUserID,
		Name:        payload.Name,
		Email:       sql.NullString{String: payload.Email, Valid: payload.Email != ""},
		Phone:       sql.NullString{String: payload.Phone, Valid: payload.Phone != ""},
		Dob:         sql.NullString{String: payload.Dob, Valid: payload.Dob != ""},
		Address:     sql.NullString{String: payload.Address, Valid: payload.Address != ""},
		Status:      payload.Status,
		Tags:        sql.NullString{String: payload.Tags, Valid: payload.Tags != ""},
		// Map new fields
		Income:        sql.NullFloat64{Float64: *payload.Income, Valid: payload.Income != nil},
		MaritalStatus: sql.NullString{String: payload.MaritalStatus, Valid: payload.MaritalStatus != ""},
		City:          sql.NullString{String: payload.City, Valid: payload.City != ""},
		JobProfile:    sql.NullString{String: payload.JobProfile, Valid: payload.JobProfile != ""},
		Dependents:    sql.NullInt64{Int64: *payload.Dependents, Valid: payload.Dependents != nil},
		Liability:     sql.NullFloat64{Float64: *payload.Liability, Valid: payload.Liability != nil},
		HousingType:   sql.NullString{String: payload.HousingType, Valid: payload.HousingType != ""},
		VehicleCount:  sql.NullInt64{Int64: *payload.VehicleCount, Valid: payload.VehicleCount != nil},
		VehicleType:   sql.NullString{String: payload.VehicleType, Valid: payload.VehicleType != ""},
		VehicleCost:   sql.NullFloat64{Float64: *payload.VehicleCost, Valid: payload.VehicleCost != nil},
	}
	clientID, err := createClient(newClient)
	if err != nil {
		log.Printf("ERROR: Failed to create client for agent %d: %v", agentUserID, err)
		respondError(w, http.StatusInternalServerError, "Failed to create client")
		return
	}
	newClient.ID = clientID
	logActivity(agentUserID, "client_added", fmt.Sprintf("Added client '%s'", newClient.Name), fmt.Sprintf("%d", clientID))
	respondJSON(w, http.StatusCreated, newClient)
}
func handleGetClient(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Could not get user ID from context")
		return
	}
	clientIDStr := chi.URLParam(r, "clientId")
	clientID, err := strconv.ParseInt(clientIDStr, 10, 64)
	if err != nil || clientID <= 0 {
		respondError(w, http.StatusBadRequest, "Invalid client ID in URL path")
		return
	}
	client, err := getClientByID(clientID, agentUserID)
	if err != nil {
		if err == sql.ErrNoRows {
			respondError(w, http.StatusNotFound, "Client not found or not owned by agent")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to retrieve client")
		return
	}
	respondJSON(w, http.StatusOK, client)
}
func handleUpdateClient(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Could not get user ID from context")
		return
	}
	clientIDStr := chi.URLParam(r, "clientId")
	clientID, err := strconv.ParseInt(clientIDStr, 10, 64)
	if err != nil || clientID <= 0 {
		respondError(w, http.StatusBadRequest, "Invalid client ID in URL path")
		return
	}
	var payload ClientPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}
	if payload.Name == "" {
		respondError(w, http.StatusBadRequest, "Client name is required")
		return
	}

	// Fetch existing client first to ensure ownership (optional but good practice)
	_, err = getClientByID(clientID, agentUserID)
	if err != nil {
		if err == sql.ErrNoRows {
			respondError(w, http.StatusNotFound, "Client not found or not owned by agent")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to retrieve client before update")
		return
	}

	updatedClient := Client{
		Name:    payload.Name,
		Email:   sql.NullString{String: payload.Email, Valid: payload.Email != ""},
		Phone:   sql.NullString{String: payload.Phone, Valid: payload.Phone != ""},
		Dob:     sql.NullString{String: payload.Dob, Valid: payload.Dob != ""},
		Address: sql.NullString{String: payload.Address, Valid: payload.Address != ""},
		Status:  payload.Status,
		Tags:    sql.NullString{String: payload.Tags, Valid: payload.Tags != ""},
		// Map new fields
		Income:        sql.NullFloat64{Float64: *payload.Income, Valid: payload.Income != nil},
		MaritalStatus: sql.NullString{String: payload.MaritalStatus, Valid: payload.MaritalStatus != ""},
		City:          sql.NullString{String: payload.City, Valid: payload.City != ""},
		JobProfile:    sql.NullString{String: payload.JobProfile, Valid: payload.JobProfile != ""},
		Dependents:    sql.NullInt64{Int64: *payload.Dependents, Valid: payload.Dependents != nil},
		Liability:     sql.NullFloat64{Float64: *payload.Liability, Valid: payload.Liability != nil},
		HousingType:   sql.NullString{String: payload.HousingType, Valid: payload.HousingType != ""},
		VehicleCount:  sql.NullInt64{Int64: *payload.VehicleCount, Valid: payload.VehicleCount != nil},
		VehicleType:   sql.NullString{String: payload.VehicleType, Valid: payload.VehicleType != ""},
		VehicleCost:   sql.NullFloat64{Float64: *payload.VehicleCost, Valid: payload.VehicleCost != nil},
	}
	err = updateClient(clientID, agentUserID, updatedClient)
	if err != nil {
		if err == sql.ErrNoRows {
			respondError(w, http.StatusNotFound, "Client not found or not owned by agent")
			return
		}
		log.Printf("ERROR: Failed to update client %d for agent %d: %v", clientID, agentUserID, err)
		respondError(w, http.StatusInternalServerError, "Failed to update client")
		return
	}
	logActivity(agentUserID, "client_updated", fmt.Sprintf("Updated client '%s'", updatedClient.Name), clientIDStr)
	respondJSON(w, http.StatusOK, map[string]string{"message": "Client updated successfully"})
}

//	func handleDeleteClient(w http.ResponseWriter, r *http.Request) {
//		agentUserID, ok := getUserIDFromContext(r.Context())
//		if !ok {
//			respondError(w, http.StatusInternalServerError, "Could not get user ID from context")
//			return
//		}
//		clientIDStr := chi.URLParam(r, "clientId")
//		clientID, err := strconv.ParseInt(clientIDStr, 10, 64)
//		if err != nil || clientID <= 0 {
//			respondError(w, http.StatusBadRequest, "Invalid client ID in URL path")
//			return
//		}
//		err = deleteClient(clientID, agentUserID)
//		if err != nil {
//			if err == sql.ErrNoRows {
//				respondError(w, http.StatusNotFound, "Client not found or not owned by agent")
//				return
//			}
//			log.Printf("ERROR: Failed to delete client %d for agent %d: %v", clientID, agentUserID, err)
//			respondError(w, http.StatusInternalServerError, "Failed to delete client")
//			return
//		}
//		respondJSON(w, http.StatusOK, map[string]string{"message": "Client deleted successfully"})
//	}
func handleGetClientPolicies(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}
	clientIDStr := chi.URLParam(r, "clientId")
	clientID, err := strconv.ParseInt(clientIDStr, 10, 64)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid client ID")
		return
	}
	policies, err := getPoliciesByClientID(clientID, agentUserID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to retrieve policies")
		return
	}
	respondJSON(w, http.StatusOK, policies)
}
func handleCreateClientPolicy(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}
	clientIDStr := chi.URLParam(r, "clientId")
	clientID, err := strconv.ParseInt(clientIDStr, 10, 64)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid client ID")
		return
	}
	var payload CreatePolicyPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}
	if payload.PolicyNumber == "" || payload.Status == "" || payload.StartDate == "" || payload.EndDate == "" {
		respondError(w, http.StatusBadRequest, "Missing required policy fields")
		return
	}
	newPolicy := Policy{ClientID: clientID, AgentUserID: agentUserID, ProductID: sql.NullString{String: payload.ProductID, Valid: payload.ProductID != ""}, PolicyNumber: payload.PolicyNumber, Insurer: payload.Insurer, Premium: payload.Premium, SumInsured: payload.SumInsured, StartDate: sql.NullString{String: payload.StartDate, Valid: payload.StartDate != ""}, EndDate: sql.NullString{String: payload.EndDate, Valid: payload.EndDate != ""}, Status: payload.Status, PolicyDocURL: sql.NullString{String: payload.PolicyDocURL, Valid: payload.PolicyDocURL != ""}}
	policyID, err := createPolicy(newPolicy)
	if err != nil {
		log.Printf("ERROR: Failed to create policy for client %d: %v", clientID, err)
		respondError(w, http.StatusInternalServerError, "Failed to create policy")
		return
	}
	newPolicy.ID = policyID
	respondJSON(w, http.StatusCreated, newPolicy)
}
func handleGetClientCommunications(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}
	clientIDStr := chi.URLParam(r, "clientId")
	clientID, err := strconv.ParseInt(clientIDStr, 10, 64)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid client ID")
		return
	}
	comms, err := getCommunicationsByClientID(clientID, agentUserID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to retrieve communications")
		return
	}
	respondJSON(w, http.StatusOK, comms)
}
func handleCreateClientCommunication(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}
	clientIDStr := chi.URLParam(r, "clientId")
	clientID, err := strconv.ParseInt(clientIDStr, 10, 64)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid client ID")
		return
	}
	var payload CreateCommunicationPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}
	if payload.Summary == "" || payload.Type == "" {
		respondError(w, http.StatusBadRequest, "Type and summary are required")
		return
	}
	timestamp, err := time.Parse(time.RFC3339, payload.Timestamp)
	if err != nil {
		timestamp = time.Now()
	}
	newComm := Communication{ClientID: clientID, AgentUserID: agentUserID, Type: payload.Type, Timestamp: timestamp, Summary: payload.Summary}
	commID, err := createCommunication(newComm)
	if err != nil {
		log.Printf("ERROR: Failed to create communication log for client %d: %v", clientID, err)
		respondError(w, http.StatusInternalServerError, "Failed to log communication")
		return
	}
	newComm.ID = commID
	respondJSON(w, http.StatusCreated, newComm)
}
func handleGetClientTasks(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}
	clientIDStr := chi.URLParam(r, "clientId")
	clientID, err := strconv.ParseInt(clientIDStr, 10, 64)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid client ID")
		return
	}
	tasks, err := getTasksByClientID(clientID, agentUserID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to retrieve tasks")
		return
	}
	respondJSON(w, http.StatusOK, tasks)
}
func handleCreateClientTask(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}
	clientIDStr := chi.URLParam(r, "clientId")
	clientID, err := strconv.ParseInt(clientIDStr, 10, 64)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid client ID")
		return
	}
	var payload CreateTaskPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}
	if payload.Description == "" {
		respondError(w, http.StatusBadRequest, "Task description is required")
		return
	}
	newTask := Task{ClientID: clientID, AgentUserID: agentUserID, Description: payload.Description, DueDate: sql.NullString{String: payload.DueDate, Valid: payload.DueDate != ""}, IsUrgent: payload.IsUrgent, IsCompleted: false}
	taskID, err := createTask(newTask)
	if err != nil {
		log.Printf("ERROR: Failed to create task for client %d: %v", clientID, err)
		respondError(w, http.StatusInternalServerError, "Failed to create task")
		return
	}
	newTask.ID = taskID

	respondJSON(w, http.StatusCreated, newTask)
}

// func handleGetAgentProfile(w http.ResponseWriter, r *http.Request) {
// 	userID, ok := getUserIDFromContext(r.Context())
// 	if !ok {
// 		respondError(w, http.StatusInternalServerError, "Auth error")
// 		return
// 	}

// 	// Fetch basic user info (we need email, createdAt, userType etc.)
// 	// We need a getUserByID function or fetch by email if email is stored in context/userInfo
// 	// Let's assume we have a way to get the basic User struct
// 	// For now, we'll just fetch the extended profile and manually add basic info
// 	// TODO: Implement getUserByID(id int64) (*User, error)
// 	// user, err := getUserByID(userID)
// 	// if err != nil { respondError(w, http.StatusInternalServerError, "Failed to fetch user details"); return }

// 	profile, err := getAgentProfile(userID)

// 	if err != nil && err != sql.ErrNoRows {
// 		respondError(w, http.StatusInternalServerError, "Failed to fetch agent profile details")
// 		return
// 	}
// 	if err == sql.ErrNoRows {
// 		// If no profile exists yet, create a default one to return
// 		profile = &AgentProfile{UserID: userID}
// 	}

// 	// Combine basic user info (placeholder for now) with extended profile
// 	fullProfile := FullAgentProfile{
// 		// User: *user, // Use fetched user data here
// 		User:         User{ID: userID, Email: "agent@example.com", UserType: "agent", CreatedAt: time.Now()}, // Placeholder user data
// 		AgentProfile: *profile,
// 	}

// 	respondJSON(w, http.StatusOK, fullProfile)
// }

// PUT /api/agents/profile
func handleUpdateAgentProfile(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}

	var payload UpdateAgentProfilePayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request payload: "+err.Error())
		return
	}

	// TODO: Add validation for payload fields (e.g., PAN format, IFSC format)

	profile := AgentProfile{
		UserID:        userID,
		Mobile:        sql.NullString{String: payload.Mobile, Valid: payload.Mobile != ""},
		Gender:        sql.NullString{String: payload.Gender, Valid: payload.Gender != ""},
		PostalAddress: sql.NullString{String: payload.PostalAddress, Valid: payload.PostalAddress != ""},
		AgencyName:    sql.NullString{String: payload.AgencyName, Valid: payload.AgencyName != ""},
		PAN:           sql.NullString{String: payload.PAN, Valid: payload.PAN != ""},
		BankName:      sql.NullString{String: payload.BankName, Valid: payload.BankName != ""},
		BankAccountNo: sql.NullString{String: payload.BankAccountNo, Valid: payload.BankAccountNo != ""},
		BankIFSC:      sql.NullString{String: payload.BankIFSC, Valid: payload.BankIFSC != ""},
	}

	err := upsertAgentProfile(profile)
	if err != nil {
		log.Printf("ERROR: Failed to update agent profile %d: %v", userID, err)
		if strings.Contains(err.Error(), "PAN number already exists") {
			respondError(w, http.StatusConflict, err.Error())
		} else {
			respondError(w, http.StatusInternalServerError, "Failed to update profile")
		}
		return
	}

	logActivity(userID, "profile_updated", "Agent profile updated", "") // Log activity
	respondJSON(w, http.StatusOK, map[string]string{"message": "Profile updated successfully"})
}

// GET /api/agents/goals
func handleGetAgentGoal(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}

	goal, err := getAgentGoal(userID)
	if err != nil && err != sql.ErrNoRows {
		respondError(w, http.StatusInternalServerError, "Failed to fetch agent goals")
		return
	}
	if err == sql.ErrNoRows {
		// Return default empty goal if none exists
		respondJSON(w, http.StatusOK, AgentGoal{UserID: userID})
		return
	}
	respondJSON(w, http.StatusOK, goal)
}

// PUT /api/agents/goals
func handleUpdateAgentGoal(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}

	var payload UpdateAgentGoalPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request payload: "+err.Error())
		return
	}

	// Validate target period format if needed
	if payload.TargetPeriod == "" {
		respondError(w, http.StatusBadRequest, "Target Period is required")
		return
	}

	goal := AgentGoal{
		UserID:       userID,
		TargetIncome: sql.NullFloat64{Float64: *payload.TargetIncome, Valid: payload.TargetIncome != nil},
		TargetPeriod: sql.NullString{String: payload.TargetPeriod, Valid: payload.TargetPeriod != ""},
	}

	err := upsertAgentGoal(goal)
	if err != nil {
		log.Printf("ERROR: Failed to update agent goal %d: %v", userID, err)
		respondError(w, http.StatusInternalServerError, "Failed to update goal")
		return
	}

	logActivity(userID, "goal_updated", fmt.Sprintf("Agent goal updated for period %s", goal.TargetPeriod.String), "")
	respondJSON(w, http.StatusOK, goal) // Return updated goal
}

func handleGetClientDocuments(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}
	clientIDStr := chi.URLParam(r, "clientId")
	clientID, err := strconv.ParseInt(clientIDStr, 10, 64)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid client ID")
		return
	}
	docs, err := getDocumentsByClientID(clientID, agentUserID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to retrieve documents")
		return
	}
	respondJSON(w, http.StatusOK, docs)
}
func handleUploadClientDocument(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}
	clientIDStr := chi.URLParam(r, "clientId")
	clientID, err := strconv.ParseInt(clientIDStr, 10, 64)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid client ID")
		return
	}
	err = r.ParseMultipartForm(10 << 20)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Error parsing form data: "+err.Error())
		return
	}
	file, handler, err := r.FormFile("file")
	if err != nil {
		respondError(w, http.StatusBadRequest, "Error retrieving the file: "+err.Error())
		return
	}
	defer file.Close()
	title := r.FormValue("title")
	documentType := r.FormValue("documentType")
	if title == "" {
		title = handler.Filename
	}
	if documentType == "" {
		documentType = "Other"
	}
	log.Printf("Received file upload: %s, Size: %d, Type: %s, Title: %s", handler.Filename, handler.Size, documentType, title)
	_ = os.MkdirAll(config.UploadPath, os.ModePerm)
	fileExt := filepath.Ext(handler.Filename)
	safeFilename := fmt.Sprintf("%d_%d_%s%s", agentUserID, clientID, generateSimpleID(8), fileExt)
	filePath := filepath.Join(config.UploadPath, safeFilename)
	dst, err := os.Create(filePath)
	if err != nil {
		log.Printf("ERROR: Unable to create file %s: %v", filePath, err)
		respondError(w, http.StatusInternalServerError, "Unable to save file")
		return
	}
	defer dst.Close()
	if _, err := io.Copy(dst, file); err != nil {
		log.Printf("ERROR: Unable to copy file %s: %v", filePath, err)
		respondError(w, http.StatusInternalServerError, "Unable to save file content")
		return
	}
	log.Printf("File saved successfully to: %s", filePath)
	newDoc := Document{ClientID: clientID, AgentUserID: agentUserID, Title: title, DocumentType: documentType, FileURL: filePath}
	docID, err := createDocument(newDoc)
	if err != nil {
		log.Printf("ERROR: Failed to create document record for client %d: %v", clientID, err)
		respondError(w, http.StatusInternalServerError, "Failed to save document metadata")
		return
	}
	newDoc.ID = docID
	respondJSON(w, http.StatusCreated, newDoc)
}
func handleGetMarketingCampaigns(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}
	campaigns, err := getMarketingCampaigns(agentUserID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to retrieve campaigns")
		return
	}
	respondJSON(w, http.StatusOK, campaigns)
}
func handleCreateMarketingCampaign(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}
	var payload CreateCampaignPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}
	if payload.Name == "" {
		respondError(w, http.StatusBadRequest, "Campaign name is required")
		return
	}
	if payload.Status == "" {
		payload.Status = "Draft"
	}
	newCampaign := MarketingCampaign{AgentUserID: agentUserID, Name: payload.Name, Status: payload.Status, TargetSegmentName: sql.NullString{String: payload.TargetSegmentName, Valid: payload.TargetSegmentName != ""}, CreatedAt: time.Now()}
	campaignID, err := createMarketingCampaign(newCampaign)
	if err != nil {
		log.Printf("ERROR: Failed to create campaign for agent %d: %v", agentUserID, err)
		respondError(w, http.StatusInternalServerError, "Failed to create campaign")
		return
	}
	newCampaign.ID = campaignID
	respondJSON(w, http.StatusCreated, newCampaign)
}
func handleGetMarketingTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := getMarketingTemplates()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to retrieve templates")
		return
	}
	respondJSON(w, http.StatusOK, templates)
}
func handleGetMarketingContent(w http.ResponseWriter, r *http.Request) {
	content, err := getMarketingContent()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to retrieve content")
		return
	}
	respondJSON(w, http.StatusOK, content)
}
func handleGetClientSegments(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}
	segments, err := getClientSegments(agentUserID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to retrieve segments")
		return
	}
	respondJSON(w, http.StatusOK, segments)
}

// Helper to calculate age from YYYY-MM-DD string
func calculateAge(dobString string) int {
	dob, err := time.Parse("2006-01-02", dobString)
	if err != nil {
		return 0
	}
	today := time.Now()
	age := today.Year() - dob.Year()
	if today.YearDay() < dob.YearDay() {
		age--
	}
	return age
}

// --- NEW: Coverage Estimation Logic ---
func estimateCoverage(client Client) CoverageEstimation {
	estimation := CoverageEstimation{
		Health: EstimatedCoverage{Amount: 0, Unit: "Lakhs", Notes: []string{}},
		Life:   EstimatedCoverage{Amount: 0, Unit: "Crores", Notes: []string{}},
		Motor:  EstimatedCoverage{Amount: 0, Unit: "IDV (₹)", Notes: []string{}},
	}

	// --- Health Estimation ---
	baseHealth := 5.0 // Base 5 Lakhs
	healthNotes := []string{"Base coverage suggested: 5 Lakhs."}

	// Factor in Income (Example: +1L for every 5L above 5L income)
	if client.Income.Valid && client.Income.Float64 > 500000 {
		incomeFactor := math.Floor((client.Income.Float64-500000)/500000) * 1.0
		baseHealth += incomeFactor
		healthNotes = append(healthNotes, fmt.Sprintf("Increased by %.0f Lakhs based on income.", incomeFactor))
	}

	// Factor in City (Example: +5L for Metro)
	if client.City.Valid && (strings.Contains(strings.ToLower(client.City.String), "mumbai") || strings.Contains(strings.ToLower(client.City.String), "delhi") || strings.Contains(strings.ToLower(client.City.String), "bangalore") || strings.Contains(strings.ToLower(client.City.String), "chennai")) {
		baseHealth += 5.0
		healthNotes = append(healthNotes, "Increased by 5 Lakhs for metro city healthcare costs.")
	}

	// Factor in Dependents (Example: +1L per dependent)
	if client.Dependents.Valid && client.Dependents.Int64 > 0 {
		depFactor := float64(client.Dependents.Int64) * 1.0
		baseHealth += depFactor
		healthNotes = append(healthNotes, fmt.Sprintf("Increased by %.0f Lakhs for %d dependents.", depFactor, client.Dependents.Int64))
	}

	// Factor in Age (Example: Suggest higher base for older clients)
	age := 0
	if client.Dob.Valid {
		age = calculateAge(client.Dob.String)
	}
	if age > 45 {
		baseHealth += 5.0 // Suggest higher base
		healthNotes = append(healthNotes, "Increased base coverage suggested due to age (>45).")
	}

	// Cap and set final health estimation
	estimation.Health.Amount = math.Min(math.Max(baseHealth, 5.0), 100.0) // Min 5L, Max 1 Cr
	estimation.Health.Notes = healthNotes

	// --- Life Estimation (Term Insurance Focus) ---
	baseLifeMultiplier := 15.0 // 15x income rule of thumb
	lifeNotes := []string{}
	estimatedLifeCover := 0.0

	if client.Income.Valid && client.Income.Float64 > 0 {
		estimatedLifeCover = client.Income.Float64 * baseLifeMultiplier
		lifeNotes = append(lifeNotes, fmt.Sprintf("Based on %.0fx income multiplier.", baseLifeMultiplier))
	} else {
		lifeNotes = append(lifeNotes, "Income data missing, cannot estimate using multiplier.")
	}

	// Add Liabilities
	if client.Liability.Valid && client.Liability.Float64 > 0 {
		estimatedLifeCover += client.Liability.Float64
		lifeNotes = append(lifeNotes, fmt.Sprintf("Added ₹%.0f for liabilities.", client.Liability.Float64))
	}

	// Convert to Crores and round
	if estimatedLifeCover > 0 {
		lifeCrores := math.Round(estimatedLifeCover/100000) / 100 // Round to 2 decimal places of Crores
		estimation.Life.Amount = math.Max(lifeCrores, 0.5)        // Suggest minimum 0.5 Cr if income allows
		lifeNotes = append(lifeNotes, "Rounded to nearest Lakh.")
		if estimation.Life.Amount < 0.5 && client.Income.Valid && client.Income.Float64 > 300000 { // Suggest minimum if income is reasonable
			estimation.Life.Amount = 0.5
			lifeNotes = append(lifeNotes, "Minimum 0.5 Cr cover suggested.")
		}
	} else {
		estimation.Life.Amount = 0 // No basis for estimation
		lifeNotes = append(lifeNotes, "Insufficient data for estimation.")
	}
	estimation.Life.Notes = lifeNotes

	// --- Motor Estimation ---
	motorNotes := []string{}
	estimatedIDV := 0.0
	if client.VehicleCost.Valid && client.VehicleCost.Float64 > 0 {
		// Simple IDV estimation (e.g., 85% of cost - very basic)
		estimatedIDV = client.VehicleCost.Float64 * 0.85
		motorNotes = append(motorNotes, fmt.Sprintf("Estimated IDV based on approx cost (₹%.0f).", client.VehicleCost.Float64))
		if client.VehicleCount.Valid && client.VehicleCount.Int64 > 1 {
			motorNotes = append(motorNotes, fmt.Sprintf("Client has %d vehicles, IDV estimate based on total cost.", client.VehicleCount.Int64))
		}
		motorNotes = append(motorNotes, "Comprehensive cover recommended.")
		estimation.Motor.Amount = math.Round(estimatedIDV)
	} else {
		motorNotes = append(motorNotes, "Vehicle cost data missing for IDV estimation.")
	}
	estimation.Motor.Notes = motorNotes

	return estimation
}

// --- NEW: Coverage Estimation Handler ---
func handleGetCoverageEstimation(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Could not get user ID from context")
		return
	}
	clientIDStr := chi.URLParam(r, "clientId")
	clientID, err := strconv.ParseInt(clientIDStr, 10, 64)
	if err != nil || clientID <= 0 {
		respondError(w, http.StatusBadRequest, "Invalid client ID in URL path")
		return
	}

	// Fetch the client data
	client, err := getClientByID(clientID, agentUserID)
	if err != nil {
		if err == sql.ErrNoRows {
			respondError(w, http.StatusNotFound, "Client not found or not owned by agent")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to retrieve client data for estimation")
		return
	}

	// Perform estimation
	estimation := estimateCoverage(*client)

	respondJSON(w, http.StatusOK, estimation)
}
func handleCreateClientSegment(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}
	var payload CreateSegmentPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}
	if payload.Name == "" {
		respondError(w, http.StatusBadRequest, "Segment name is required")
		return
	}
	newSegment := ClientSegment{AgentUserID: agentUserID, Name: payload.Name, Criteria: sql.NullString{String: payload.Criteria, Valid: payload.Criteria != ""}}
	segmentID, err := createClientSegment(newSegment)
	if err != nil {
		log.Printf("ERROR: Failed to create segment for agent %d: %v", agentUserID, err)
		respondError(w, http.StatusInternalServerError, "Failed to create segment")
		return
	}
	newSegment.ID = segmentID
	respondJSON(w, http.StatusCreated, newSegment)
}
func getCommissionRecords(agentUserID int64, dateRangeStart, dateRangeEnd string) ([]Policy, error) {
	log.Printf("DATABASE: Fetching commission records for agent %d (Range: %s - %s)\n", agentUserID, dateRangeStart, dateRangeEnd)

	// We select from policies table, joining clients for name, filtering by agent and date range
	// Date range filtering can be on policy creation date (created_at) or start date etc. Let's use created_at for now.
	query := `SELECT
				p.id, p.client_id, p.agent_user_id, p.product_id, p.policy_number, p.insurer,
				p.premium, p.sum_insured, p.start_date, p.end_date, p.status, p.policy_doc_url,
				p.upfront_commission_amount, p.created_at, p.updated_at,
				c.name as client_name -- Include client name
			  FROM policies p
			  JOIN clients c ON p.client_id = c.id
			  WHERE p.agent_user_id = ?`
	args := []interface{}{agentUserID}

	// Add date range filter if provided (assuming YYYY-MM-DD format)
	if dateRangeStart != "" {
		query += " AND p.created_at >= ?"
		args = append(args, dateRangeStart+" 00:00:00") // Start of the day
	}
	if dateRangeEnd != "" {
		query += " AND p.created_at <= ?"
		args = append(args, dateRangeEnd+" 23:59:59") // End of the day
	}

	query += " ORDER BY p.created_at DESC" // Order by policy creation date

	rows, err := db.Query(query, args...)
	if err != nil {
		log.Printf("ERROR: Query commission records failed: %v", err)
		return nil, err
	}
	defer rows.Close()

	var records []Policy // Reusing Policy struct, might need a dedicated CommissionRecord struct later
	for rows.Next() {
		var p Policy
		var clientName sql.NullString // To scan the joined client name
		// Scan including the new commission amount and client name
		if err := rows.Scan(
			&p.ID, &p.ClientID, &p.AgentUserID, &p.ProductID, &p.PolicyNumber, &p.Insurer,
			&p.Premium, &p.SumInsured, &p.StartDate, &p.EndDate, &p.Status, &p.PolicyDocURL,
			&p.UpfrontCommissionAmount, &p.CreatedAt, &p.UpdatedAt, &clientName,
		); err != nil {
			log.Printf("ERROR: Scan commission record row failed: %v", err)
			continue
		}
		// We might want to add clientName to the Policy struct or create a new struct
		// For now, we are fetching it but not directly using it in the return struct `p`
		log.Printf("Fetched commission record for policy %s, client %s", p.PolicyNumber, clientName.String)
		records = append(records, p)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}
func handleGetCommissions(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Authentication error: User ID not found in token")
		return
	}

	// Get filters from query parameters
	// Example: ?startDate=2025-04-01&endDate=2025-04-30
	startDate := r.URL.Query().Get("startDate")
	endDate := r.URL.Query().Get("endDate")
	// TODO: Add other filters like status (paid/pending) if needed

	records, err := getCommissionRecords(agentUserID, startDate, endDate)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to retrieve commission records")
		return
	}

	respondJSON(w, http.StatusOK, records)
}

func productsHandler(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Could not get user ID from context")
		return
	}
	// Check if DB was initialized
	if db == nil {
		log.Println("ERROR: Database connection is not available for /api/products")
		http.Error(w, "Database connection not configured", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// --- Data Source: Database Query ---
	// IMPORTANT: Replace 'your_products_table' with your actual table name.
	// Ensure columns 'id' and 'name' exist and match the Product struct fields.
	query := `SELECT product_id, name FROM agent_insurer_relations WHERE agent_user_id = ?`
	rows, err := db.Query(query, agentUserID)
	if err != nil {
		log.Printf("Error querying database for products: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	// IMPORTANT: Defer closing rows to prevent resource leaks
	defer rows.Close()

	// --- Scan Results ---
	products := []AgentInsurerRelation{} // Initialize an empty slice to hold results
	for rows.Next() {                    // Iterate through each row returned
		var p AgentInsurerRelation // Create a temporary Product struct

		// Scan the values from the current row into the fields.
		// Assumes 'id' and 'name' columns are NOT NULL in the DB.
		// If they can be NULL, update Product struct to use sql.NullString
		// and scan accordingly (like in clientsHandler).
		err := rows.Scan(&p.ID, &p.Name)
		if err != nil {
			log.Printf("Error scanning product database row: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return // Stop processing if scanning fails
		}
		// Append the successfully scanned product to the slice
		products = append(products, p)
	}

	// Check for errors that may have occurred during row iteration
	if err = rows.Err(); err != nil {
		log.Printf("Error iterating product database rows: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// --- Encode and Send Response ---
	err = json.NewEncoder(w).Encode(products) // Encode the slice fetched from DB
	if err != nil {
		log.Printf("Error encoding products to JSON: %v", err)
		// Avoid sending another http.Error if headers are already sent
		// Consider just logging here if encoding fails after starting response
	}
	log.Printf("GET /api/products request served successfully from DB at %s", time.Now().Format(time.RFC3339)) // Updated log
}

func handleGetClients(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Could not get user ID from context")
		return
	}
	statusFilter := r.URL.Query().Get("status")
	searchTerm := r.URL.Query().Get("search")
	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")
	limit, _ := strconv.Atoi(limitStr)
	offset, _ := strconv.Atoi(offsetStr)
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	if offset < 0 {
		offset = 0
	}
	clients, err := getClientsByAgentID(agentUserID, statusFilter, searchTerm, limit, offset)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to retrieve clients")
		return
	}
	respondJSON(w, http.StatusOK, clients)
}
func getUserByID(userID int64) (*User, error) {
	log.Printf("DATABASE: Getting user by ID: %d\n", userID)
	row := db.QueryRow("SELECT id, email, password_hash, user_type, is_verified, created_at FROM users WHERE id = ?", userID)
	user := &User{}
	err := row.Scan(&user.ID, &user.Email, &user.PasswordHash, &user.UserType, &user.IsVerified, &user.CreatedAt)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("ERROR: Failed to scan user row for ID %d: %v\n", userID, err)
		} else {
			log.Printf("DATABASE: User not found: %d\n", userID)
		}
		return nil, err
	}
	return user, nil
}

// func handleGetAgentProfile(w http.ResponseWriter, r *http.Request) {
// 	userID, ok := getUserIDFromContext(r.Context())
// 	if !ok {
// 		respondError(w, http.StatusInternalServerError, "Auth error")
// 		return
// 	}

// 	// Fetch basic user info (requires getUserByID or similar)
// 	// Placeholder: Assume we get basic user info
// 	// TODO: Implement getUserByID
// 	user_data, err := getUserByID(userID)
// 	if err != nil {
// 		respondError(w, http.StatusInternalServerError, "Failed to fetch user details")
// 		return
// 	}
// 	user := User{ID: userID, Email: user_data.Email, UserType: user_data.UserType, CreatedAt: user_data.CreatedAt} // Placeholder

// 	// Fetch extended profile
// 	profile, err := getAgentProfile(userID)
// 	if err != nil && err != sql.ErrNoRows {
// 		respondError(w, http.StatusInternalServerError, "Failed to fetch agent profile details")
// 		return
// 	}
// 	if err == sql.ErrNoRows {
// 		profile = &AgentProfile{UserID: userID}
// 	} // Default empty profile if none exists

// 	// Fetch Insurer POCs
// 	pocs, err := getAgentInsurerPOCs(userID)
// 	if err != nil {
// 		log.Printf("WARN: Failed to fetch insurer POCs for agent %d: %v", userID, err)
// 		pocs = []AgentInsurerPOC{}
// 	} // Don't fail request if POCs error

// 	// Combine into the new response struct
// 	fullProfile := FullAgentProfileWithPOCs{
// 		User:         user, // Use fetched user data here eventually
// 		AgentProfile: *profile,
// 		InsurerPOCs:  pocs,
// 	}

// 	respondJSON(w, http.StatusOK, fullProfile)
// }

func getDashboardMetrics(agentUserID int64) (*DashboardMetrics, error) {
	metrics := &DashboardMetrics{}
	now := time.Now()
	firstOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	firstOfNextMonth := firstOfMonth.AddDate(0, 1, 0)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	thirtyDaysFromNow := today.AddDate(0, 0, 30)
	sevenDaysAgo := today.AddDate(0, 0, -7)

	// Policies Sold This Month
	err := db.QueryRow(`SELECT COUNT(*) FROM policies WHERE agent_user_id = ? AND created_at >= ? AND created_at < ?`,
		agentUserID, firstOfMonth, firstOfNextMonth).Scan(&metrics.PoliciesSoldThisMonth)
	if err != nil && err != sql.ErrNoRows {
		log.Printf("ERROR: DB metrics policies sold: %v", err)
		return nil, err
	}

	// Upcoming Renewals (Next 30 days)
	err = db.QueryRow(`SELECT COUNT(*) FROM policies WHERE agent_user_id = ? AND status = 'Active' AND end_date >= ? AND end_date < ?`,
		agentUserID, today, thirtyDaysFromNow).Scan(&metrics.UpcomingRenewals30d)
	if err != nil && err != sql.ErrNoRows {
		log.Printf("ERROR: DB metrics renewals: %v", err)
		return nil, err
	}

	// Commission Earned This Month
	var commissionThisMonth *float64
	err = db.QueryRow(`SELECT SUM(upfront_commission_amount) FROM policies WHERE agent_user_id = ? AND created_at >= ? AND created_at < ?`,
		agentUserID, firstOfMonth, firstOfNextMonth).Scan(&commissionThisMonth)
	if err != nil && err != sql.ErrNoRows {
		log.Printf("ERROR: DB metrics commission: %v", err)
		return nil, err
	}

	// Handle the case where there's no commission this month (NULL value)
	if commissionThisMonth != nil {
		metrics.CommissionThisMonth = *commissionThisMonth
	} else {
		metrics.CommissionThisMonth = 0.0 // Or any other appropriate default value
	}

	// New Leads This Week
	err = db.QueryRow(`SELECT COUNT(*) FROM clients WHERE agent_user_id = ? AND status = 'Lead' AND created_at >= ?`,
		agentUserID, sevenDaysAgo).Scan(&metrics.NewLeadsThisWeek)
	if err != nil && err != sql.ErrNoRows {
		log.Printf("ERROR: DB metrics new leads: %v", err)
		return nil, err
	}

	log.Printf("DATABASE: Fetched dashboard metrics for agent %d", agentUserID)
	return metrics, nil
}
func handleGetDashboardMetrics(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}
	metrics, err := getDashboardMetrics(agentUserID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to retrieve dashboard metrics")
		return
	}
	respondJSON(w, http.StatusOK, metrics)
}

// Updated getTasksByClientID to be getAgentTasks (more general for dashboard)
func getAgentTasks(agentUserID int64, limit int) ([]Task, error) {
	log.Printf("DATABASE: Fetching pending tasks for agent %d (Limit: %d)\n", agentUserID, limit)
	rows, err := db.Query(`SELECT id, client_id, agent_user_id, description, due_date, is_urgent, is_completed, created_at, completed_at
                            FROM tasks WHERE agent_user_id = ? AND is_completed = 0
                           ORDER BY is_urgent DESC, ISNULL(due_date) ASC, due_date ASC, created_at DESC LIMIT ?`, agentUserID, limit)
	if err != nil {
		log.Printf("ERROR: Query tasks failed: %v", err)
		return nil, err
	}
	defer rows.Close()
	var tasks []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.ClientID, &t.AgentUserID, &t.Description, &t.DueDate, &t.IsUrgent, &t.IsCompleted, &t.CreatedAt, &t.CompletedAt); err != nil {
			log.Printf("ERROR: Scan task row failed: %v", err)
			continue
		}
		tasks = append(tasks, t)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return tasks, nil
}

// NEW: Log Activity Function
func logActivity(agentUserID int64, activityType, description, relatedID string) {
	log.Printf("ACTIVITY LOG: User %d, Type: %s, Desc: %s, Related: %s", agentUserID, activityType, description, relatedID)
	go func() { // Run in goroutine to avoid blocking main request flow
		stmt, err := db.Prepare(`INSERT INTO activity_log (agent_user_id, activity_type, description, related_id) VALUES (?, ?, ?, ?)`)
		if err != nil {
			log.Printf("ERROR: Prepare logActivity stmt: %v", err)
			return
		}
		defer stmt.Close()
		_, err = stmt.Exec(agentUserID, activityType, description, relatedID)
		if err != nil {
			log.Printf("ERROR: Execute logActivity insert: %v", err)
		}
	}()
}

// NEW: Get Recent Activity Function
func getRecentActivity(agentUserID int64, limit int) ([]ActivityLog, error) {
	log.Printf("DATABASE: Fetching recent activity for agent %d (Limit: %d)\n", agentUserID, limit)
	rows, err := db.Query(`SELECT id, agent_user_id, timestamp, activity_type, description, related_id
                           FROM activity_log WHERE agent_user_id = ?
                           ORDER BY timestamp DESC LIMIT ?`, agentUserID, limit)
	if err != nil {
		log.Printf("ERROR: Query activity log failed: %v", err)
		return nil, err
	}
	defer rows.Close()
	var activities []ActivityLog
	for rows.Next() {
		var a ActivityLog
		var related sql.NullString // Handle potentially null related_id
		if err := rows.Scan(&a.ID, &a.AgentUserID, &a.Timestamp, &a.ActivityType, &a.Description, &related); err != nil {
			log.Printf("ERROR: Scan activity log row failed: %v", err)
			continue
		}
		if related.Valid {
			a.RelatedID = related.String
		}
		activities = append(activities, a)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return activities, nil
}

func handleGetDashboardTasks(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}
	// Get limit from query param, default to 5
	limitStr := r.URL.Query().Get("limit")
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 {
		limit = 5
	}
	tasks, err := getAgentTasks(agentUserID, limit) // Using the renamed function
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to retrieve tasks")
		return
	}
	respondJSON(w, http.StatusOK, tasks)
}
func handleGetDashboardActivity(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}
	// Get limit from query param, default to 5
	limitStr := r.URL.Query().Get("limit")
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 {
		limit = 5
	}
	activities, err := getRecentActivity(agentUserID, limit)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to retrieve recent activity")
		return
	}
	respondJSON(w, http.StatusOK, activities)
}
func storePortalToken(token string, clientID int64, agentUserID int64, duration time.Duration) error {
	log.Printf("DATABASE: Storing portal token for client %d (agent %d)\n", clientID, agentUserID)
	expiresAt := time.Now().Add(duration)
	// Using token directly as PK, assuming it's unique enough (generate securely)
	stmt, err := db.Prepare("INSERT INTO client_portal_tokens (token, client_id, agent_user_id, expires_at) VALUES (?, ?, ?, ?)")
	if err != nil {
		return fmt.Errorf("failed to prepare store portal token: %w", err)
	}
	defer stmt.Close()
	_, err = stmt.Exec(token, clientID, agentUserID, expiresAt)
	if err != nil {
		return fmt.Errorf("failed to execute store portal token: %w", err)
	}
	log.Printf("DATABASE: Portal token stored successfully\n")
	return nil
}
func handleGeneratePortalLink(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}
	clientIDStr := chi.URLParam(r, "clientId")
	clientID, err := strconv.ParseInt(clientIDStr, 10, 64)
	if err != nil || clientID <= 0 {
		respondError(w, http.StatusBadRequest, "Invalid client ID")
		return
	}
	// Verify client belongs to agent
	_, err = getClientByID(clientID, agentUserID)
	if err != nil {
		if err == sql.ErrNoRows {
			respondError(w, http.StatusNotFound, "Client not found or not owned by agent")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to verify client ownership")
		return
	}

	// Generate unique token
	token, err := generateToken(32) // Use a secure random token
	if err != nil {
		log.Printf("ERROR: Failed to generate portal token: %v", err)
		respondError(w, http.StatusInternalServerError, "Failed to generate link token")
		return
	}

	// Store token with expiry (e.g., 7 days)
	duration := 7 * 24 * time.Hour
	err = storePortalToken(token, clientID, agentUserID, duration)
	if err != nil {
		log.Printf("ERROR: Failed to store portal token: %v", err)
		respondError(w, http.StatusInternalServerError, "Failed to save link token")
		return
	}

	// Construct the full URL
	// Ensure config.FrontendURL doesn't have a trailing slash and path starts with one
	portalPath := "/client-portal/" + token
	fullURL, err := url.JoinPath(config.FrontendURL, portalPath)
	if err != nil {
		log.Printf("ERROR: Failed to join portal URL: %v", err)
		respondError(w, http.StatusInternalServerError, "Failed to construct portal link")
		return
	}

	log.Printf("Generated portal link for client %s by agent %d", fullURL, agentUserID)
	respondJSON(w, http.StatusOK, map[string]string{"portalLink": fullURL})
}

// // GET /api/portal/client/{token} (Public)
// func handleGetPublicClientData(w http.ResponseWriter, r *http.Request) {
// 	token := chi.URLParam(r, "token")
// 	if token == "" {
// 		respondError(w, http.StatusBadRequest, "Missing access token")
// 		return
// 	}

// 	// Verify token and get IDs
// 	clientID, agentUserID, err := verifyPortalToken(token)
// 	if err != nil {
// 		if err == sql.ErrNoRows {
// 			respondError(w, http.StatusNotFound, "Invalid or expired link")
// 			return
// 		}
// 		respondError(w, http.StatusInternalServerError, "Error validating link")
// 		return
// 	}

// 	// Fetch required data using the verified IDs
// 	client, err := getClientByID(clientID, agentUserID) // Use agentID from token
// 	if err != nil {
// 		if err == sql.ErrNoRows {
// 			respondError(w, http.StatusNotFound, "Client data not found")
// 			return
// 		}
// 		respondError(w, http.StatusInternalServerError, "Failed to retrieve client data")
// 		return
// 	}

// 	policies, err := getPoliciesByClientID(clientID, agentUserID)
// 	if err != nil {
// 		log.Printf("WARN: Failed to fetch policies for portal view (Client %d): %v", clientID, err)
// 		policies = []Policy{}
// 	} // Don't fail request if policies fail

// 	documents, err := getDocumentsByClientID(clientID, agentUserID)
// 	if err != nil {
// 		log.Printf("WARN: Failed to fetch documents for portal view (Client %d): %v", clientID, err)
// 		documents = []Document{}
// 	} // Don't fail request if docs fail

// 	// Construct public view
// 	publicView := PublicClientView{
// 		Name:      client.Name,
// 		Email:     client.Email.String, // Only include if valid? Or always show? Let's show if present.
// 		Phone:     client.Phone.String,
// 		Policies:  policies,
// 		Documents: documents,
// 		// Add other fields as needed
// 	}

// 	respondJSON(w, http.StatusOK, publicView)
// }

// POST /api/portal/client/{token}/documents (Public)
func handlePublicDocumentUpload(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		respondError(w, http.StatusBadRequest, "Missing access token")
		return
	}

	// Verify token and get IDs
	clientID, agentUserID, err := verifyPortalToken(token)
	if err != nil {
		if err == sql.ErrNoRows {
			respondError(w, http.StatusNotFound, "Invalid or expired link")
			return
		}
		respondError(w, http.StatusInternalServerError, "Error validating link")
		return
	}

	// --- Handle File Upload (Similar to authenticated version) ---
	err = r.ParseMultipartForm(10 << 20) // 10 MB limit
	if err != nil {
		respondError(w, http.StatusBadRequest, "Error parsing form data: "+err.Error())
		return
	}
	file, handler, err := r.FormFile("file")
	if err != nil {
		respondError(w, http.StatusBadRequest, "Error retrieving the file: "+err.Error())
		return
	}
	defer file.Close()
	title := r.FormValue("title")
	documentType := r.FormValue("documentType")
	if title == "" {
		title = handler.Filename
	}
	if documentType == "" {
		documentType = "Other"
	}
	log.Printf("PORTAL UPLOAD: Received file: %s, Size: %d, Type: %s, Title: %s for Client %d", handler.Filename, handler.Size, documentType, title, clientID)

	_ = os.MkdirAll(config.UploadPath, os.ModePerm)
	fileExt := filepath.Ext(handler.Filename)
	safeFilename := fmt.Sprintf("%d_%d_%s%s", agentUserID, clientID, generateSimpleID(8), fileExt)
	filePath := filepath.Join(config.UploadPath, safeFilename)
	dst, err := os.Create(filePath)
	if err != nil {
		log.Printf("ERROR: Unable to create file %s: %v", filePath, err)
		respondError(w, http.StatusInternalServerError, "Unable to save file")
		return
	}
	defer dst.Close()
	if _, err := io.Copy(dst, file); err != nil {
		log.Printf("ERROR: Unable to copy file %s: %v", filePath, err)
		respondError(w, http.StatusInternalServerError, "Unable to save file content")
		return
	}
	log.Printf("PORTAL UPLOAD: File saved successfully to: %s", filePath)

	// Save metadata to database, associating with the correct client and agent
	newDoc := Document{ClientID: clientID, AgentUserID: agentUserID, Title: title, DocumentType: documentType, FileURL: filePath}
	docID, err := createDocument(newDoc)
	if err != nil {
		log.Printf("ERROR: Failed to create document record for client %d from portal: %v", clientID, err)
		respondError(w, http.StatusInternalServerError, "Failed to save document details")
		return
	}
	newDoc.ID = docID

	// Log activity (optional)
	logActivity(agentUserID, "doc_uploaded_portal", fmt.Sprintf("Client uploaded document '%s'", newDoc.Title), fmt.Sprintf("%d", clientID))

	respondJSON(w, http.StatusCreated, newDoc) // Return created document info
}
func verifyPortalToken(token string) (clientID int64, agentUserID int64, err error) {
	log.Printf("DATABASE: Verifying portal token\n")
	row := db.QueryRow("SELECT client_id, agent_user_id FROM client_portal_tokens WHERE token = ? AND expires_at > ?", token, time.Now())
	err = row.Scan(&clientID, &agentUserID)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("ERROR: Failed to scan portal token row: %v\n", err)
		} else {
			log.Printf("DATABASE: Portal token not found or expired\n")
		}
		return 0, 0, err // Return specific error (sql.ErrNoRows or other)
	}
	log.Printf("DATABASE: Portal token verified for client %d (agent %d)\n", clientID, agentUserID)
	return clientID, agentUserID, nil
}
func handleSuggestClientTasks(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}
	clientIDStr := chi.URLParam(r, "clientId")
	clientID, err := strconv.ParseInt(clientIDStr, 10, 64)
	if err != nil || clientID <= 0 {
		respondError(w, http.StatusBadRequest, "Invalid client ID")
		return
	}

	// 1. Fetch required data for prompt
	client, err := getClientByID(clientID, agentUserID)
	if err != nil {
		respondError(w, http.StatusNotFound, "Client not found or not accessible")
		return
	}

	// Fetch recent communications (e.g., last 5)
	recentComms, err := getCommunicationsByClientID(clientID, agentUserID) // Assumes this function exists and limits results reasonably
	if err != nil {
		log.Printf("WARN: Failed to get recent comms for task suggestion (Client %d): %v", clientID, err) /* Continue anyway */
	}

	// 2. Construct Prompt
	var promptBuilder strings.Builder
	promptBuilder.WriteString(fmt.Sprintf("Analyze the following insurance client profile and recent interactions to suggest 1-3 specific follow-up tasks for the agent. Client: %s.", client.Name))
	if client.Status != "" {
		promptBuilder.WriteString(fmt.Sprintf(" Status: %s.", client.Status))
	}
	// Add other relevant client details sparingly
	if len(recentComms) > 0 {
		promptBuilder.WriteString(" Recent communications (newest first):")
		limit := 3 // Limit number of comms in prompt
		for i, comm := range recentComms {
			if i >= limit {
				break
			}
			promptBuilder.WriteString(fmt.Sprintf(" (%s - %s: %s)", comm.Timestamp.Format("2006-01-02"), comm.Type, comm.Summary))
		}
		promptBuilder.WriteString(".")
	} else {
		promptBuilder.WriteString(" No recent communications logged.")
	}
	// Add request for JSON output
	promptBuilder.WriteString(" Provide the suggested tasks strictly in JSON format as an array of objects, like this: ")
	promptBuilder.WriteString(`[{"description": "Task description...","clientID":"client id ", "dueDate": "YYYY-MM-DD", "isUrgent": false}, {"description": "Another task...", "dueDate": "", "isUrgent": true}]`)
	promptText := promptBuilder.String()
	print(promptText, "promptText promptText")
	log.Printf("AI TASK SUGGEST: Sending prompt for client %d", clientID)
	// log.Println("Prompt:", promptText) // Optional: Log full prompt for debugging

	// 3. Call Google AI API
	// if config.GoogleAiApiKey == "AIzaSyAoIOupDd4VBbcJMob0tTlaiGOTsP3AqXg" {
	// 	respondError(w, http.StatusInternalServerError, "AI service is not configured")
	// 	return
	// }

	geminiURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-1.5-flash:generateContent?key=%s", "config.GoogleAiApiKeyAIzaSyAoIOupDd4VBbcJMob0tTlaiGOTsP3AqXg")
	requestPayload := GeminiRequest{
		Contents: []GeminiContent{{Parts: []GeminiPart{{Text: promptText}}}},
		// Optional: Configure generation parameters for more structured output
		GenerationConfig: &GeminiGenerationConfig{Temperature: 1, MaxOutputTokens: 500},
	}
	payloadBytes, err := json.Marshal(requestPayload)
	if err != nil {
		log.Printf("ERROR: Marshalling Gemini request: %v", err)
		respondError(w, http.StatusInternalServerError, "Error preparing AI request")
		return
	}

	resp, err := http.Post(geminiURL, "application/json", bytes.NewBuffer(payloadBytes))
	if err != nil {
		log.Printf("ERROR: Calling Gemini API: %v", err)
		respondError(w, http.StatusServiceUnavailable, "Error contacting AI service")
		return
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("ERROR: Reading Gemini response: %v", err)
		respondError(w, http.StatusInternalServerError, "Error reading AI response")
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("ERROR: Gemini API non-OK status: %d, Body: %s", resp.StatusCode, string(bodyBytes))
		respondError(w, http.StatusBadGateway, fmt.Sprintf("AI service returned error: %s", resp.Status))
		return
	}

	// 4. Parse AI Response
	var geminiResp GeminiResponse
	if err := json.Unmarshal(bodyBytes, &geminiResp); err != nil {
		log.Printf("ERROR: Unmarshalling Gemini response: %v\nBody: %s", err, string(bodyBytes))
		respondError(w, http.StatusInternalServerError, "Error parsing AI response")
		return
	}

	var suggestedTasks []SuggestedTask
	createdCount := 0
	if len(geminiResp.Candidates) > 0 && len(geminiResp.Candidates[0].Content.Parts) > 0 {
		aiText := geminiResp.Candidates[0].Content.Parts[0].Text
		log.Printf("AI TASK SUGGEST: Raw AI response text: %s", aiText)

		// Attempt to extract JSON array from the response text
		// This is fragile and depends on the AI strictly following instructions
		startIndex := strings.Index(aiText, "[")
		endIndex := strings.LastIndex(aiText, "]")
		if startIndex != -1 && endIndex != -1 && endIndex > startIndex {
			jsonArrayString := aiText[startIndex : endIndex+1]
			if err := json.Unmarshal([]byte(jsonArrayString), &suggestedTasks); err != nil {
				log.Printf("WARN: Failed to parse JSON array from AI response: %v. Raw text: %s", err, aiText)
				// Could try more lenient parsing or just fail here
			}
		} else {
			log.Printf("WARN: Could not find JSON array brackets '[]' in AI response: %s", aiText)
		}

	} else {
		log.Println("WARN: No candidates or parts found in Gemini response.")
	}

	// 5. Create Tasks in DB
	if len(suggestedTasks) > 0 {
		log.Printf("AI TASK SUGGEST: Parsed %d suggested tasks. Attempting to create.", len(suggestedTasks))
		for _, st := range suggestedTasks {
			if st.Description == "" {
				continue
			} // Skip tasks without description

			newTask := Task{
				ClientID:    clientID,
				AgentUserID: agentUserID,
				Description: st.Description,
				DueDate:     sql.NullString{String: st.DueDate, Valid: st.DueDate != ""},
				IsUrgent:    st.IsUrgent,
				IsCompleted: false,
			}
			_, err := createTask(newTask)
			if err != nil {
				log.Printf("ERROR: Failed to create suggested task for client %d: %v. Task: %+v", clientID, err, st)
				// Continue trying to add other tasks
			} else {
				createdCount++
				logActivity(agentUserID, "task_suggested", fmt.Sprintf("AI suggested task '%s'", newTask.Description), fmt.Sprintf("%d", clientID))
			}
		}
	} else {
		log.Println("AI TASK SUGGEST: No valid tasks parsed from AI response.")
	}

	// 6. Respond Success
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message":        fmt.Sprintf("AI analysis complete. %d new tasks suggested and added.", createdCount),
		"suggestionsRaw": geminiResp.Candidates[0].Content.Parts[0].Text, // Optionally return raw AI text for frontend display
	})
}

func handleSuggestAgentTasks(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error: User ID missing")
		return
	}

	log.Printf("AI TASK SUGGEST (Agent %d): Starting process...", agentUserID)

	// 1. Fetch Summary Data for Prompt
	// Get client counts
	clients, err := getClientCountsByStatus(agentUserID)
	print("client Data", clients)
	leadCount := 0
	activeCount := 0
	lapsedCount := 0
	for _, client := range clients {
		switch client.Status {
		case "lead":
			leadCount++
		case "active":
			activeCount++
		case "lapsed":
			lapsedCount++
		}
	}
	fmt.Printf("Lead Count: %d, Active Count: %d, Lapsed Count: %d\n", leadCount, activeCount, lapsedCount)
	totalClients := len(clients) // To get the total number of clients

	// You can then use these counts as needed.
	// TODO: Fetch other relevant summary data if needed (e.g., upcoming renewals count, clients without recent contact)
	// For simplicity, we'll just use counts now.

	// 2. Construct Prompt
	clientSummary := fmt.Sprintf("The agent currently has %d clients (%d leads, %d active).", totalClients, leadCount, activeCount)
	// Optionally add agent's goal if available
	goal, _ := getAgentGoal(agentUserID) // Ignore error for goal, it's optional context
	goalText := ""
	if goal != nil && goal.TargetIncome.Valid && goal.TargetPeriod.Valid {
		goalText = fmt.Sprintf(" The agent's current income goal is ₹%.0f for the period %s.", goal.TargetIncome.Float64, goal.TargetPeriod.String)
	}

	promptText := fmt.Sprintf("I am an insurance agent using ClientWise CRM. %s%s Based on this portfolio overview and goal,  identify which clients should i reach out to and why, to increase my business with my leads and active clients. Study the client profile, his existing and recommended insurance coverage, communication and task logs and . Format the output strictly as a JSON array of objects: `[{\"description\": \"...\", \"ClientID\": 123 (mandatory), \"dueDate\": \"YYYY-MM-DD\" (mandatory), \"isUrgent\": false}]`",
		clientSummary,
		goalText,
	)

	log.Printf("AI TASK SUGGEST (Agent %d): Sending prompt...", agentUserID)
	// log.Println("Prompt:", promptText) // DEBUG

	// 3. Call Google AI API
	// if config.GoogleAiApiKey == "" {
	// 	respondError(w, http.StatusInternalServerError, "AI service is not configured")
	// 	return
	// }
	geminiURL := "https://generativelanguage.googleapis.com/v1beta/models/gemini-1.5-flash:generateContent?key=AIzaSyAoIOupDd4VBbcJMob0tTlaiGOTsP3AqXg"
	requestPayload := GeminiRequest{
		Contents:         []GeminiContent{{Parts: []GeminiPart{{Text: promptText}}}},
		GenerationConfig: &GeminiGenerationConfig{Temperature: 0.6, MaxOutputTokens: 300}, // Configured for task list
	}
	payloadBytes, err := json.Marshal(requestPayload)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Error preparing AI request")
		return
	}
	resp, err := http.Post(geminiURL, "application/json", bytes.NewBuffer(payloadBytes))
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, "Error contacting AI service")
		return
	}
	print("respp", resp)
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Error reading AI response")
		return
	}
	if resp.StatusCode != http.StatusOK {
		log.Printf("ERROR: Gemini API non-OK status: %d, Body: %s", resp.StatusCode, string(bodyBytes))
		respondError(w, http.StatusBadGateway, fmt.Sprintf("AI service returned error: %s", resp.Status))
		return
	}

	// 4. Parse AI Response
	var geminiResp GeminiResponse
	if err := json.Unmarshal(bodyBytes, &geminiResp); err != nil {
		log.Printf("ERROR: Unmarshalling Gemini response: %v\nBody: %s", err, string(bodyBytes))
		respondError(w, http.StatusInternalServerError, "Error parsing AI response")
		return
	}

	var suggestedTasks []SuggestedTask
	aiRawText := ""
	log.Print(geminiResp.Candidates, "geminiResp.Candidates")
	if len(geminiResp.Candidates) > 0 && len(geminiResp.Candidates[0].Content.Parts) > 0 {
		aiRawText = geminiResp.Candidates[0].Content.Parts[0].Text
		log.Printf("AI TASK SUGGEST (Agent %d): Raw AI response text: %s", agentUserID, aiRawText)
		// Attempt to extract JSON array - more robust parsing might be needed
		startIndex := strings.Index(aiRawText, "[")
		endIndex := strings.LastIndex(aiRawText, "]")
		if startIndex != -1 && endIndex != -1 && endIndex > startIndex {
			jsonArrayString := aiRawText[startIndex : endIndex+1]
			print(jsonArrayString, "jsonArrayString")
			if err := json.Unmarshal([]byte(jsonArrayString), &suggestedTasks); err != nil {
				log.Printf("WARN: Failed to parse JSON array from AI response: %v. Raw text: %s", err, aiRawText)
			}
		} else {
			log.Printf("WARN: Could not find JSON array brackets '[]' in AI response: %s", aiRawText)
		}
	} else {
		log.Println("WARN: No candidates or parts found in Gemini response.")
	}

	// 5. Create Tasks in DB
	createdCount := 0
	if len(suggestedTasks) > 0 {
		log.Printf("AI TASK SUGGEST (Agent %d): Parsed %d tasks. Attempting to create.", agentUserID, len(suggestedTasks))
		for _, st := range suggestedTasks {
			if st.Description == "" {
				continue
			}
			// Determine clientId for the task, default to a sentinel or handle based on context
			// Here, we require the AI to explicitly provide a valid clientId if the task is client-specific
			var taskClientId int64 = 0 // Default: Task is not linked to a specific client
			if st.ClientID != nil {
				// OptionClientIDal: Verify this client ID actually belongs to the agent before creating task?
				// _, err := getClientByID(*st.ClientID, agentUserID)
				// if err == nil { taskClientId = *st.ClientID } else { log.Printf("WARN: AI suggested task for client %d not owned by agent %d, unlinking task.", *st.ClientID, agentUserID) }
				taskClientId = *st.ClientID // For now, trust the AI if it provides one
			} else {
				// If AI doesn't provide clientId, we MUST ensure the tasks table allows NULL client_id
				// Let's modify the DB schema/logic slightly: Assume tasks MUST link to a client.
				// We need to modify the prompt to ALWAYS return a clientId or make clientId nullable.
				// Reverting: Keep task ClientID NOT NULL for now, AI must associate or task ignored if clientId is needed.
				// For simplicity, let's require clientId from AI for now.
				if taskClientId == 0 {
					log.Printf("WARN: AI suggested task '%s' without a client ID, skipping.", st.Description)
					continue // Skip task if no client ID provided by AI
				}
			}

			newTask := Task{
				ClientID:    taskClientId, // Use the ID from AI suggestion
				AgentUserID: agentUserID,
				Description: st.Description,
				DueDate:     sql.NullString{String: st.DueDate, Valid: st.DueDate != ""},
				IsUrgent:    st.IsUrgent,
				IsCompleted: false,
			}
			_, err := createTask(newTask) // Uses existing function
			if err != nil {
				log.Printf("ERROR: Failed to create suggested task for client %d: %v. Task: %+v", taskClientId, err, st)
			} else {
				createdCount++
			}
		}
	} else {
		log.Println("AI TASK SUGGEST: No valid tasks parsed from AI response.")
	}

	// 6. Respond Success
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message":        fmt.Sprintf("AI analysis complete. %d new tasks suggested and added.", createdCount),
		"suggestionsRaw": aiRawText, // Return raw AI text for frontend display/debugging
	})
}

func handleGetRenewals(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}

	daysStr := r.URL.Query().Get("days")
	days, err := strconv.Atoi(daysStr)
	if err != nil || days <= 0 {
		days = 30 // Default to 30 days
	}

	renewals, err := getUpcomingRenewals(agentUserID, days)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to retrieve upcoming renewals")
		return
	}
	respondJSON(w, http.StatusOK, renewals)
}

// Update /api/task/staus
func handleUpdateTaskStatus(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusUnauthorized, "Authentication failed")
		return
	}

	var req struct {
		TaskID int64  `json:"taskId"`
		Status string `json:"status"` // "pending" or "completed"
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	var (
		query  string
		args   []interface{}
		now    = time.Now()
		result sql.Result
		err    error
	)

	if req.Status == "completed" {
		query = `
			UPDATE tasks
			SET status = ?, updated_at = ?, completed_at = ?
			WHERE id = ? AND user_id = ?
		`
		args = []interface{}{req.Status, now, now, req.TaskID, agentUserID}
	} else {
		query = `
			UPDATE tasks
			SET status = ?, updated_at = ?, completed_at = NULL
			WHERE id = ? AND user_id = ?
		`
		args = []interface{}{req.Status, now, req.TaskID, agentUserID}
	}

	result, err = db.Exec(query, args...)
	if err != nil {
		log.Println("Error updating task status:", err)
		respondError(w, http.StatusInternalServerError, "Failed to update task status")
		return
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil || rowsAffected == 0 {
		respondError(w, http.StatusNotFound, "No task found or unauthorized")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"message": "Task status updated successfully"})
}

// GET /api/tasks
func handleGetAllTasks(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}

	// Filters & Pagination
	statusFilter := r.URL.Query().Get("status") // "all", "pending", "completed"
	pageStr := r.URL.Query().Get("page")
	page, _ := strconv.Atoi(pageStr)
	if page <= 0 {
		page = 1
	}
	pageSizeStr := r.URL.Query().Get("limit")
	pageSize, _ := strconv.Atoi(pageSizeStr)
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	tasks, totalItems, err := getAllAgentTasks(agentUserID, statusFilter, page, pageSize)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to retrieve tasks")
		return
	}

	totalPages := int(math.Ceil(float64(totalItems) / float64(pageSize)))
	response := PaginatedResponse{
		Items: tasks, TotalItems: totalItems, CurrentPage: page, PageSize: pageSize, TotalPages: totalPages,
	}
	respondJSON(w, http.StatusOK, response)
}

// GET /api/activity
func handleGetFullActivityLog(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}

	// Pagination
	pageStr := r.URL.Query().Get("page")
	page, _ := strconv.Atoi(pageStr)
	if page <= 0 {
		page = 1
	}
	pageSizeStr := r.URL.Query().Get("limit")
	pageSize, _ := strconv.Atoi(pageSizeStr)
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 50
	}

	activities, totalItems, err := getFullActivityLog(agentUserID, page, pageSize)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to retrieve activity log")
		return
	}

	totalPages := int(math.Ceil(float64(totalItems) / float64(pageSize)))
	response := PaginatedResponse{
		Items: activities, TotalItems: totalItems, CurrentPage: page, PageSize: pageSize, TotalPages: totalPages,
	}
	respondJSON(w, http.StatusOK, response)
}

func handleUpdateAgentInsurerPOCs(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}

	var payload UpdateInsurerPOCsPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request payload: "+err.Error())
		return
	}

	// Basic validation (e.g., limit size, check email formats)
	if len(payload.POCs) > 6 {
		respondError(w, http.StatusBadRequest, "Cannot save more than 6 insurer contacts.")
		return
	}
	// TODO: Add email format validation for each poc.PocEmail

	err := setAgentInsurerPOCs(userID, payload.POCs)
	if err != nil {
		log.Printf("ERROR: Failed to update insurer POCs for agent %d: %v", userID, err)
		respondError(w, http.StatusInternalServerError, "Failed to update insurer contacts")
		return
	}

	logActivity(userID, "insurer_pocs_updated", "Agent insurer contacts updated", "")
	respondJSON(w, http.StatusOK, map[string]string{"message": "Insurer contacts updated successfully"})
}

// func handleSendProposalEmail(w http.ResponseWriter, r *http.Request) {
// 	agentUserID, ok := getUserIDFromContext(r.Context())
// 	if !ok {
// 		respondError(w, http.StatusInternalServerError, "Auth error")
// 		return
// 	}

// 	var payload SendProposalPayload
// 	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
// 		respondError(w, http.StatusBadRequest, "Invalid request payload")
// 		return
// 	}
// 	if payload.ClientID <= 0 || payload.ProductID == "" {
// 		respondError(w, http.StatusBadRequest, "Client ID and Product ID are required")
// 		return
// 	}

// 	// 1. Fetch Client Details (and verify ownership)
// 	client, err := getClientByID(payload.ClientID, agentUserID)
// 	if err != nil {
// 		if err == sql.ErrNoRows {
// 			respondError(w, http.StatusNotFound, "Client not found or not owned by agent")
// 			return
// 		}
// 		respondError(w, http.StatusInternalServerError, "Failed to retrieve client details")
// 		return
// 	}

// 	// 2. Fetch Product Details
// 	product, err := getProductByID(payload.ProductID)
// 	if err != nil {
// 		if err == sql.ErrNoRows {
// 			respondError(w, http.StatusNotFound, "Product not found")
// 			return
// 		}
// 		respondError(w, http.StatusInternalServerError, "Failed to retrieve product details")
// 		return
// 	}

// 	// 3. Fetch Agent's POC Email for the Insurer
// 	poc, err := getAgentInsurerPOCByInsurer(agentUserID, product.Insurer)
// 	if err != nil {
// 		if err == sql.ErrNoRows {
// 			respondError(w, http.StatusBadRequest, fmt.Sprintf("No Point of Contact email saved in your profile for insurer '%s'. Please update your profile.", product.Insurer))
// 			return
// 		}
// 		respondError(w, http.StatusInternalServerError, "Failed to retrieve insurer contact details")
// 		return
// 	}
// 	if poc.PocEmail == "" { // Should be caught by UNIQUE constraint + DB func check ideally
// 		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Stored POC email for '%s' is empty.", product.Insurer))
// 		return
// 	}

// 	// 4. Construct Email
// 	// TODO: Enhance email body with more details, maybe HTML format
// 	subject := fmt.Sprintf("Insurance Proposal Request for Client: %s", client.Name)
// 	body := fmt.Sprintf("Proposal Request from Agent ID: %d\n\n", agentUserID)
// 	body += fmt.Sprintf("Client Details:\nName: %s\n", client.Name)
// 	if client.Email.Valid {
// 		body += fmt.Sprintf("Email: %s\n", client.Email.String)
// 	}
// 	if client.Phone.Valid {
// 		body += fmt.Sprintf("Phone: %s\n", client.Phone.String)
// 	}
// 	body += fmt.Sprintf("\nRequested Product:\nID: %s\nName: %s\nCategory: %s\nInsurer: %s\n",
// 		product.ID, product.Name, product.Category, product.Insurer)
// 	if product.PremiumIndication.Valid {
// 		body += fmt.Sprintf("Premium Indication: %s\n", product.PremiumIndication.String)
// 	}
// 	// Add more details as needed

// 	// 5. Send Email (Using Mock for now)
// 	recipients := []string{poc.PocEmail} // Create a slice containing the single email

// 	err = sendEmail(recipients, subject, body)
// 	if err != nil {
// 		log.Printf("ERROR: Failed to send proposal email to %s for agent %d: %v", poc.PocEmail, agentUserID, err)
// 		// Don't necessarily expose email failure details to frontend
// 		respondError(w, http.StatusServiceUnavailable, "Failed to send proposal email. Please try again later.")
// 		return
// 	}

// 	// 6. Log Activity
// 	logActivity(agentUserID, "proposal_sent", fmt.Sprintf("Proposal sent for client '%s' (Product: %s) to %s", client.Name, product.Name, product.Insurer), fmt.Sprintf("%d", client.ID))

// 	// 7. Respond Success
// 	respondJSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("Proposal request for '%s' sent successfully to %s.", client.Name, product.Insurer)})
// }

func handleGetClientSegment(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}
	segmentIDStr := chi.URLParam(r, "segmentId")
	segmentID, err := strconv.ParseInt(segmentIDStr, 10, 64)
	if err != nil || segmentID <= 0 {
		respondError(w, http.StatusBadRequest, "Invalid segment ID")
		return
	}

	segment, err := getClientSegmentByID(segmentID, agentUserID)
	if err != nil {
		if err == sql.ErrNoRows {
			respondError(w, http.StatusNotFound, "Segment not found or not owned by agent")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to retrieve segment")
		return
	}
	respondJSON(w, http.StatusOK, segment)
}

// PUT /api/marketing/segments/{segmentId}
func handleUpdateClientSegment(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}
	segmentIDStr := chi.URLParam(r, "segmentId")
	segmentID, err := strconv.ParseInt(segmentIDStr, 10, 64)
	if err != nil || segmentID <= 0 {
		respondError(w, http.StatusBadRequest, "Invalid segment ID")
		return
	}

	var payload UpdateSegmentPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}
	if payload.Name == "" {
		respondError(w, http.StatusBadRequest, "Segment name is required")
		return
	}

	// Construct segment object for update function
	segment := ClientSegment{
		ID:          segmentID,
		AgentUserID: agentUserID, // Ensure update is scoped to the agent
		Name:        payload.Name,
		Criteria:    sql.NullString{String: payload.Criteria, Valid: payload.Criteria != ""},
		// ClientCount is not updated here
	}

	err = updateClientSegment(segment)
	if err != nil {
		if err == sql.ErrNoRows {
			respondError(w, http.StatusNotFound, "Segment not found or not owned by agent")
			return
		}
		log.Printf("ERROR: Failed to update segment %d for agent %d: %v", segmentID, agentUserID, err)
		respondError(w, http.StatusInternalServerError, "Failed to update segment")
		return
	}

	logActivity(agentUserID, "segment_updated", fmt.Sprintf("Updated segment '%s'", segment.Name), fmt.Sprintf("%d", segmentID))
	respondJSON(w, http.StatusOK, map[string]string{"message": "Segment updated successfully"})
}
func handleBulkClientUpload(w http.ResponseWriter, r *http.Request) {

	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}

	// 1. Parse Multipart Form
	// Max upload size (e.g., 5MB) - adjust as needed
	err := r.ParseMultipartForm(5 << 20)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Error parsing form data: "+err.Error())
		return
	}

	// 2. Get File
	file, handler, err := r.FormFile("clientFile") // "clientFile" must match the name attribute in the frontend form input
	if err != nil {
		respondError(w, http.StatusBadRequest, "Error retrieving the file ('clientFile' field missing or invalid): "+err.Error())
		return
	}
	defer file.Close()

	// 3. Validate File Type (Basic check for CSV)
	if !strings.HasSuffix(strings.ToLower(handler.Filename), ".csv") {
		respondError(w, http.StatusBadRequest, "Invalid file type. Please upload a CSV file.")
		return
	}
	log.Printf("BULK UPLOAD (Agent %d): Received file: %s, Size: %d", agentUserID, handler.Filename, handler.Size)

	// 4. Read CSV Data
	reader := csv.NewReader(file)
	// Optional: Set options like comma delimiter, lazy quotes etc. if needed
	// reader.Comma = ','
	// reader.LazyQuotes = true

	// Read header row (assuming first row is header)
	header, err := reader.Read()
	if err == io.EOF {
		respondError(w, http.StatusBadRequest, "CSV file is empty.")
		return
	}
	if err != nil {
		log.Printf("ERROR reading CSV header: %v", err)
		respondError(w, http.StatusBadRequest, "Error reading CSV header.")
		return
	}

	// Define expected header columns (case-insensitive check is good)
	// IMPORTANT: The order here dictates how we map columns later
	expectedHeaders := map[string]int{
		"name": -1, "email": -1, "phone": -1, "dob": -1, "address": -1, "status": -1, "tags": -1,
		"income": -1, "maritalstatus": -1, "city": -1, "jobprofile": -1, "dependents": -1,
		"liability": -1, "housingtype": -1, "vehiclecount": -1, "vehicletype": -1, "vehiclecost": -1,
	}
	headerMap := make(map[int]string) // Map column index to normalized header name
	for i, h := range header {
		normalizedHeader := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(h), " ", ""))
		if _, exists := expectedHeaders[normalizedHeader]; exists {
			expectedHeaders[normalizedHeader] = i // Store column index
			headerMap[i] = normalizedHeader
		}
	}

	// Check if essential headers are present
	if expectedHeaders["name"] == -1 || (expectedHeaders["email"] == -1 && expectedHeaders["phone"] == -1) {
		respondError(w, http.StatusBadRequest, "CSV must contain 'Name' column and at least one of 'Email' or 'Phone' columns.")
		return
	}

	// 5. Process Rows within a Transaction
	result := BulkUploadResult{SuccessCount: 0, FailureCount: 0, Errors: []string{}}
	tx, err := db.Begin()
	if err != nil {
		log.Printf("ERROR starting transaction: %v", err)
		respondError(w, http.StatusInternalServerError, "Database error")
		return
	}
	defer tx.Rollback() // Rollback by default, commit only on success

	// Prepare statement for insertion (more efficient than preparing in loop)
	// Note: Column order MUST match the order of fields passed to Exec later
	insertSQL := `INSERT INTO clients (
		agent_user_id, name, email, phone, dob, address, status, tags,
		income, marital_status, city, job_profile, dependents, liability, housing_type,
		vehicle_count, vehicle_type, vehicle_cost, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		log.Printf("ERROR preparing bulk insert statement: %v", err)
		respondError(w, http.StatusInternalServerError, "Database error preparing insert")
		return
	}
	defer stmt.Close()

	rowIndex := 1 // Start from 1 (after header)
	for {
		rowIndex++
		record, err := reader.Read()
		if err == io.EOF {
			break
		} // End of file
		if err != nil {
			errorMsg := fmt.Sprintf("Row %d: Error reading row - %v", rowIndex, err)
			log.Println(errorMsg)
			result.Errors = append(result.Errors, errorMsg)
			result.FailureCount++
			continue // Skip to next row
		}

		// Map record fields based on headerMap
		client := Client{AgentUserID: agentUserID, Status: "Lead", CreatedAt: time.Now()} // Default status
		for i, value := range record {
			headerName, found := headerMap[i]
			if !found {
				continue
			} // Skip columns not in our expected map

			// Assign value based on header name
			switch headerName {
			case "name":
				client.Name = strings.TrimSpace(value)
			case "email":
				client.Email = sql.NullString{String: strings.TrimSpace(value), Valid: strings.TrimSpace(value) != ""}
			case "phone":
				client.Phone = sql.NullString{String: strings.TrimSpace(value), Valid: strings.TrimSpace(value) != ""}
			case "dob":
				client.Dob = sql.NullString{String: strings.TrimSpace(value), Valid: strings.TrimSpace(value) != ""}
			case "address":
				client.Address = sql.NullString{String: strings.TrimSpace(value), Valid: strings.TrimSpace(value) != ""}
			case "status":
				if s := strings.TrimSpace(value); s != "" {
					client.Status = s
				} // Use default 'Lead' if empty
			case "tags":
				client.Tags = sql.NullString{String: strings.TrimSpace(value), Valid: strings.TrimSpace(value) != ""}
			case "income":
				client.Income = parseFloatOrNull(value)
			case "maritalstatus":
				client.MaritalStatus = sql.NullString{String: strings.TrimSpace(value), Valid: strings.TrimSpace(value) != ""}
			case "city":
				client.City = sql.NullString{String: strings.TrimSpace(value), Valid: strings.TrimSpace(value) != ""}
			case "jobprofile":
				client.JobProfile = sql.NullString{String: strings.TrimSpace(value), Valid: strings.TrimSpace(value) != ""}
			case "dependents":
				client.Dependents = parseIntOrNull(value)
			case "liability":
				client.Liability = parseFloatOrNull(value)
			case "housingtype":
				client.HousingType = sql.NullString{String: strings.TrimSpace(value), Valid: strings.TrimSpace(value) != ""}
			case "vehiclecount":
				client.VehicleCount = parseIntOrNull(value)
			case "vehicletype":
				client.VehicleType = sql.NullString{String: strings.TrimSpace(value), Valid: strings.TrimSpace(value) != ""}
			case "vehiclecost":
				client.VehicleCost = parseFloatOrNull(value)
			}
		}

		// Validate essential data for this row
		if client.Name == "" {
			errorMsg := fmt.Sprintf("Row %d: Missing required field 'Name'.", rowIndex)
			result.Errors = append(result.Errors, errorMsg)
			result.FailureCount++
			continue
		}
		if !client.Email.Valid && !client.Phone.Valid {
			errorMsg := fmt.Sprintf("Row %d: Missing required field (Email or Phone).", rowIndex)
			result.Errors = append(result.Errors, errorMsg)
			result.FailureCount++
			continue
		}

		// Execute prepared statement
		_, err = stmt.Exec(
			client.AgentUserID, client.Name, client.Email, client.Phone, client.Dob, client.Address,
			client.Status, client.Tags, client.Income, client.MaritalStatus, client.City,
			client.JobProfile, client.Dependents, client.Liability, client.HousingType,
			client.VehicleCount, client.VehicleType, client.VehicleCost, client.CreatedAt,
		)
		if err != nil {
			errorMsg := fmt.Sprintf("Row %d (Client: %s): Database error - %v", rowIndex, client.Name, err)
			// Check for unique constraint violation specifically
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				errorMsg = fmt.Sprintf("Row %d (Client: %s): Duplicate email or phone for this agent.", rowIndex, client.Name)
			}
			log.Println(errorMsg)
			result.Errors = append(result.Errors, errorMsg)
			result.FailureCount++
			// Decide whether to continue or rollback entire batch on DB error
			// For now, let's continue processing other rows but the transaction will be rolled back later if any DB error occurred.
			// If we wanted partial success, we wouldn't use a transaction or would handle errors differently.
			// Let's actually rollback immediately on DB error for atomicity.
			log.Printf("Rolling back transaction due to error on row %d", rowIndex)
			tx.Rollback() // Explicit rollback
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("Database error processing row %d. No clients were imported.", rowIndex))
			return
		} else {
			result.SuccessCount++
		}
	} // End row processing loop

	// 6. Commit Transaction if no DB errors occurred during inserts
	if err = tx.Commit(); err != nil {
		log.Printf("ERROR committing transaction: %v", err)
		// This case might happen if there was a deferred error, though we tried to handle insert errors above.
		respondError(w, http.StatusInternalServerError, "Database error finalizing import.")
		return
	}

	// 7. Return Summary
	log.Printf("BULK UPLOAD (Agent %d): Finished. Success: %d, Failed: %d", agentUserID, result.SuccessCount, result.FailureCount)
	respondJSON(w, http.StatusOK, result)
}

func getAgentInsurerDetails(agentUserID int64) ([]AgentInsurerDetail, error) {
	log.Printf("DATABASE: Getting insurer details for agent %d\n", agentUserID)
	rows, err := db.Query(`SELECT id, agent_user_id, insurer_name, agent_code, spoc_email, commission_percentage
                       FROM agent_insurer_details WHERE agent_user_id = ? ORDER BY insurer_name ASC`, agentUserID)
	if err != nil {
		log.Printf("ERROR: Query agent insurer details failed: %v", err)
		return nil, err
	}
	defer rows.Close()

	details := []AgentInsurerDetail{}
	for rows.Next() {
		var detail AgentInsurerDetail
		if err := rows.Scan(&detail.ID, &detail.AgentUserID, &detail.InsurerName, &detail.AgentCode, &detail.SpocEmail, &detail.CommissionPercentage); err != nil {
			log.Printf("ERROR: Scan agent insurer detail row failed: %v", err)
			continue
		}
		details = append(details, detail)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return details, nil
}

// Replaces all existing insurer details for the agent with the provided list
func setAgentInsurerDetails(agentUserID int64, details []AgentInsurerDetail) error {
	log.Printf("DATABASE: Setting insurer details for agent %d (count: %d)\n", agentUserID, len(details))
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Delete existing details for the agent
	_, err = tx.Exec("DELETE FROM agent_insurer_details WHERE agent_user_id = ?", agentUserID)
	if err != nil {
		return fmt.Errorf("failed to delete existing insurer details: %w", err)
	}

	// 2. Insert new details (limit to a reasonable number, e.g., 20?)
	stmt, err := tx.Prepare(`INSERT INTO agent_insurer_details
        (agent_user_id, insurer_name, agent_code, spoc_email, commission_percentage)
        VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("failed to prepare insert insurer detail: %w", err)
	}
	defer stmt.Close()

	insertCount := 0
	maxDetails := 20 // Set a practical limit
	for i, detail := range details {
		if i >= maxDetails {
			log.Printf("WARN: Attempted to save more than %d insurer details for agent %d. Truncating.", maxDetails, agentUserID)
			break
		}
		// Basic validation
		if detail.InsurerName == "" {
			continue
		} // Insurer name is mandatory

		_, err = stmt.Exec(agentUserID, detail.InsurerName, detail.AgentCode, detail.SpocEmail, detail.CommissionPercentage)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				log.Printf("WARN: Duplicate insurer name '%s' skipped for agent %d.", detail.InsurerName, agentUserID)
				continue // Skip duplicate
			}
			return fmt.Errorf("failed to insert insurer detail for '%s': %w", detail.InsurerName, err)
		}
		insertCount++
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	log.Printf("DATABASE: Successfully set %d insurer details for agent %d\n", insertCount, agentUserID)
	return nil
}

// Gets details for a specific insurer for an agent (used for proposal email)
func getAgentInsurerDetailByInsurer(agentUserID int64, insurerName string) (*AgentInsurerDetail, error) {
	log.Printf("DATABASE: Getting details for agent %d, insurer '%s'\n", agentUserID, insurerName)
	row := db.QueryRow(`SELECT id, agent_user_id, insurer_name, agent_code, spoc_email, commission_percentage
                       FROM agent_insurer_details
                       WHERE agent_user_id = ? AND LOWER(insurer_name) = LOWER(?)`,
		agentUserID, insurerName) // Case-insensitive match
	detail := &AgentInsurerDetail{}
	err := row.Scan(&detail.ID, &detail.AgentUserID, &detail.InsurerName, &detail.AgentCode, &detail.SpocEmail, &detail.CommissionPercentage)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, sql.ErrNoRows
		}
		log.Printf("ERROR: Failed to scan agent insurer detail row for insurer '%s': %v\n", insurerName, err)
		return nil, err
	}
	return detail, nil
}

func handleGetPublicClientData(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		respondError(w, http.StatusBadRequest, "Missing access token")
		return
	}

	// Verify token and get IDs
	clientID, agentUserID, err := verifyPortalToken(token)
	if err != nil {
		if err == sql.ErrNoRows {
			respondError(w, http.StatusNotFound, "Invalid or expired link")
			return
		}
		respondError(w, http.StatusInternalServerError, "Error validating link")
		return
	}

	// Fetch required data using the verified IDs
	client, err := getClientByID(clientID, agentUserID)
	if err != nil {
		if err == sql.ErrNoRows {
			respondError(w, http.StatusNotFound, "Client data not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to retrieve client data")
		return
	}

	policies, err := getPoliciesByClientID(clientID, agentUserID)
	if err != nil {
		log.Printf("WARN: Failed to fetch policies for portal view (Client %d): %v", clientID, err)
		policies = []Policy{}
	}

	documents, err := getDocumentsByClientID(clientID, agentUserID)
	if err != nil {
		log.Printf("WARN: Failed to fetch documents for portal view (Client %d): %v", clientID, err)
		documents = []Document{}
	}

	// Fetch Communications
	communications, err := getCommunicationsByClientID(clientID, agentUserID)
	if err != nil {
		log.Printf("WARN: Failed to fetch communications for portal view (Client %d): %v", clientID, err)
		communications = []Communication{}
	}

	// Calculate Coverage Estimation
	estimation := estimateCoverage(*client)

	// Fetch AI Recommendation
	aiRecText, err := fetchAiRecommendationForClient(*client, estimation)
	if err != nil {
		log.Printf("WARN: Failed to fetch AI recommendation for portal view (Client %d): %v", clientID, err)
		aiRecText = "Could not generate AI recommendations at this time."
	}

	// Construct public view with ALL required data
	publicView := PublicClientView{
		Client:             *client, // Include full client object
		Policies:           policies,
		Documents:          documents,
		Communications:     communications,
		CoverageEstimation: estimation,
		AiRecommendation:   aiRecText,
	}

	respondJSON(w, http.StatusOK, publicView)
}

func handleGetUniqueInsurers(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`SELECT DISTINCT insurer FROM products WHERE insurer IS NOT NULL AND insurer != ''`)
	if err != nil {
		log.Printf("ERROR: Failed to fetch distinct insurers: %v\n", err)
		respondError(w, http.StatusInternalServerError, "Failed to fetch insurers")
		return
	}
	defer rows.Close()

	var insurers []string
	for rows.Next() {
		var insurer string
		if err := rows.Scan(&insurer); err != nil {
			log.Printf("ERROR: Failed to scan insurer: %v\n", err)
			continue
		}
		insurers = append(insurers, insurer)
	}

	if err = rows.Err(); err != nil {
		log.Printf("ERROR: Iteration error: %v\n", err)
		respondError(w, http.StatusInternalServerError, "Error processing insurers")
		return
	}

	respondJSON(w, http.StatusOK, map[string][]string{"insurers": insurers})
}

func handleGetAgentProfile(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserIDFromContext(r.Context())
	if !ok { /* ... */
	}
	user, err := getUserByID(userID)
	if err != nil { /* ... */
	}
	profile, err := getAgentProfile(userID)
	if err != nil && err != sql.ErrNoRows { /* ... */
	}
	if err == sql.ErrNoRows {
		profile = &AgentProfile{UserID: userID}
	}
	// Fetch Insurer Relations
	relations, err := getAgentInsurerRelations(userID)
	if err != nil {
		log.Printf("WARN: Failed to fetch insurer relations for agent %d: %v", userID, err)
		relations = []AgentInsurerRelation{}
	}

	fullProfile := FullAgentProfileWithRelations{User: *user, AgentProfile: *profile, InsurerRelations: relations}
	respondJSON(w, http.StatusOK, fullProfile)
}

// (Other handlers: PUT profile, GET/PUT goals, etc.)
// ...

// NEW: Handler to update Insurer Details (replaces POC handler)
// PUT /api/agents/insurer-details
func handleUpdateAgentInsurerRelations(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserIDFromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req UpdateInsurerRelationsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid payload: %v", err), http.StatusBadRequest)
		return
	}

	var relations []AgentInsurerRelation
	now := time.Now()

	for _, payload := range req.Relations {
		insurer := strings.TrimSpace(payload.InsurerName)
		if insurer == "" {
			continue
		}

		rows, err := db.Query(`SELECT
			id, name, category, description, status, features, eligibility, term, exclusions,
			room_rent, premium_indication, insurer_logo_url, brochure_url, wording_url, claim_form_url
			FROM products
			WHERE TRIM(LOWER(insurer)) = TRIM(LOWER(?))`, insurer)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to fetch products for insurer '%s': %v", insurer, err), http.StatusInternalServerError)
			return
		}
		defer rows.Close() // Close rows here

		for rows.Next() {
			var p Product
			if err := rows.Scan(
				&p.ID, &p.Name, &p.Category, &p.Description, &p.Status, &p.Features, &p.Eligibility,
				&p.Term, &p.Exclusions, &p.RoomRent, &p.PremiumIndication, &p.InsurerLogoURL,
				&p.BrochureURL, &p.WordingURL, &p.ClaimFormURL,
			); err != nil {
				http.Error(w, fmt.Sprintf("Failed to read product row: %v", err), http.StatusInternalServerError)
				return
			}

			// Trim any newlines or spaces and parse updated_at from string to time.Time
			relations = append(relations, AgentInsurerRelation{
				AgentUserID:                 userID,
				InsurerName:                 insurer,
				AgentCode:                   sql.NullString{String: payload.AgentCode, Valid: payload.AgentCode != ""},
				SpocEmail:                   sql.NullString{String: payload.SpocEmail, Valid: payload.SpocEmail != ""},
				UpfrontCommissionPercentage: sql.NullFloat64{Float64: payload.UpfrontCommissionPercentage, Valid: true},
				TrailCommissionPercentage:   sql.NullFloat64{Float64: payload.TrailCommissionPercentage, Valid: true},
				Name:                        p.Name,
				Category:                    p.Category,
				Description:                 p.Description,
				Status:                      p.Status,
				Features:                    p.Features,
				Eligibility:                 p.Eligibility,
				Term:                        p.Term,
				Exclusions:                  p.Exclusions,
				RoomRent:                    p.RoomRent,
				PremiumIndication:           p.PremiumIndication,
				InsurerLogoURL:              p.InsurerLogoURL,
				BrochureURL:                 p.BrochureURL,
				WordingURL:                  p.WordingURL,
				ClaimFormURL:                p.ClaimFormURL,
				CreatedAt:                   now,
				ProductID:                   p.ID,
			})
		}
		if err := rows.Err(); err != nil {
			http.Error(w, fmt.Sprintf("Error during rows iteration: %v", err), http.StatusInternalServerError)
			return
		}
	}

	if len(relations) == 0 {
		http.Error(w, "No products found for the given insurers", http.StatusBadRequest)
		return
	}

	if err := setAgentInsurerRelations(userID, relations); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save agent-insurer relations: %v", err), http.StatusInternalServerError)
		return
	}

	logActivity(userID, "insurer_relations_updated", "Agent insurer relations updated", "")
	respondJSON(w, http.StatusOK, map[string]string{"message": "Insurer relations updated successfully"})
}

// UPDATED: Proposal Email Handler (uses new DB function for SPOC)
func handleSendProposalEmail(w http.ResponseWriter, r *http.Request) {
	agentUserID, ok := getUserIDFromContext(r.Context())
	if !ok { /* ... */
	}
	var payload SendProposalPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil { /* ... */
	}
	if payload.ClientID <= 0 || payload.ProductID == "" { /* ... */
	}
	client, err := getClientByID(payload.ClientID, agentUserID)
	if err != nil { /* ... */
	}
	product, err := getProductByID(payload.ProductID)
	if err != nil { /* ... */
	}

	// Fetch Agent's Insurer Relation for the product's insurer
	relation, err := getAgentInsurerRelationByInsurer(agentUserID, product.Insurer)
	if err != nil {
		if err == sql.ErrNoRows {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("Please add '%s' to your Insurer Management list in your profile, including the SPOC email.", product.Insurer))
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to retrieve insurer contact details")
		return
	}
	if !relation.SpocEmail.Valid || relation.SpocEmail.String == "" {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("No SPOC Email saved in your profile for insurer '%s'. Please update your profile.", product.Insurer))
		return
	}
	pocEmail := relation.SpocEmail.String

	// Construct Email
	subject := fmt.Sprintf("Insurance Proposal Request for Client: %s", client.Name)
	body := fmt.Sprintf("Proposal Request from Agent ID: %d\n", agentUserID)
	if relation.AgentCode.Valid && relation.AgentCode.String != "" {
		body += fmt.Sprintf("Agent Code: %s\n", relation.AgentCode.String)
	}
	body += fmt.Sprintf("\nClient Details:\nName: %s\n", client.Name)
	// ... (add more client/product details to body) ...

	// Send Email (Mocked)
	// pocEmail = str
	err = sendEmail([]string{pocEmail}, subject, body)
	if err != nil { /* ... handle email error ... */
	}

	logActivity(agentUserID, "proposal_sent", fmt.Sprintf("Proposal sent for client '%s' (Product: %s) to %s", client.Name, product.Name, product.Insurer), fmt.Sprintf("%d", client.ID))
	respondJSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("Proposal request for '%s' sent successfully to %s.", client.Name, product.Insurer)})
}

// UPDATED: Get Products Handler (adds agent filter)
func handleGetProducts(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}
	categoryFilter := r.URL.Query().Get("category")
	insurerFilter := r.URL.Query().Get("insurer")
	searchTerm := r.URL.Query().Get("search")
	// agentIdStr := r.URL.Query().Get("agentId")
	// agentIdFilter, _ := strconv.ParseInt(agentIdStr, 10, 64)
	products, err := getProducts(userID, categoryFilter, insurerFilter, searchTerm) // Pass agentIdFilter
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to retrieve products")
		return
	}
	respondJSON(w, http.StatusOK, products)
}

func handleUpdateAgentInsurerDetails(w http.ResponseWriter, r *http.Request) {
	userID, ok := getUserIDFromContext(r.Context())
	if !ok {
		respondError(w, http.StatusInternalServerError, "Auth error")
		return
	}

	var payload UpdateInsurerDetailsPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request payload: "+err.Error())
		return
	}

	// Basic validation (e.g., limit size, check email formats)
	maxDetails := 20 // Match DB limit if any
	if len(payload.Details) > maxDetails {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Cannot save more than %d insurer details.", maxDetails))
		return
	}
	// TODO: Add email format validation for each detail.SpocEmail

	err := setAgentInsurerDetails(userID, payload.Details)
	if err != nil {
		log.Printf("ERROR: Failed to update insurer details for agent %d: %v", userID, err)
		respondError(w, http.StatusInternalServerError, "Failed to update insurer details")
		return
	}

	logActivity(userID, "insurer_details_updated", "Agent insurer details updated", "")
	respondJSON(w, http.StatusOK, map[string]string{"message": "Insurer details updated successfully"})
}

// // UPDATED: Proposal Email Handler (uses new DB function)
// func handleSendProposalEmail(w http.ResponseWriter, r *http.Request) {
// 	agentUserID, ok := getUserIDFromContext(r.Context())
// 	if !ok { /* ... */
// 	}
// 	var payload SendProposalPayload
// 	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil { /* ... */
// 	}
// 	if payload.ClientID <= 0 || payload.ProductID == "" { /* ... */
// 	}

// 	client, err := getClientByID(payload.ClientID, agentUserID)
// 	if err != nil { /* ... */
// 	}
// 	product, err := getProductByID(payload.ProductID)
// 	if err != nil { /* ... */
// 	}

// 	// Fetch Agent's Insurer Detail for the product's insurer
// 	detail, err := getAgentInsurerDetailByInsurer(agentUserID, product.Insurer)
// 	if err != nil {
// 		if err == sql.ErrNoRows {
// 			respondError(w, http.StatusBadRequest, fmt.Sprintf("No contact details saved in your profile for insurer '%s'. Please update your profile.", product.Insurer))
// 			return
// 		}
// 		respondError(w, http.StatusInternalServerError, "Failed to retrieve insurer contact details")
// 		return
// 	}
// 	if !detail.SpocEmail.Valid || detail.SpocEmail.String == "" {
// 		respondError(w, http.StatusBadRequest, fmt.Sprintf("No SPOC Email saved in your profile for insurer '%s'. Please update your profile.", product.Insurer))
// 		return
// 	}
// 	pocEmail := detail.SpocEmail.String

// 	// Construct Email (using client, product, maybe agent code from detail)
// 	subject := fmt.Sprintf("Insurance Proposal Request for Client: %s", client.Name)
// 	body := fmt.Sprintf("Proposal Request from Agent ID: %d\n", agentUserID)
// 	if detail.AgentCode.Valid && detail.AgentCode.String != "" {
// 		body += fmt.Sprintf("Agent Code: %s\n", detail.AgentCode.String)
// 	}
// 	body += fmt.Sprintf("\nClient Details:\nName: %s\n", client.Name)
// 	// ... (add more client/product details to body) ...

// 	// Send Email (Mocked)
// 	recipientList := []string{pocEmail} // Create a slice with pocEmail as the only element
// 	err = sendEmail(recipientList, subject, body)
// 	if err != nil { /* ... handle email error ... */
// 	}

// 	logActivity(agentUserID, "proposal_sent", fmt.Sprintf("Proposal sent for client '%s' (Product: %s) to %s", client.Name, product.Name, product.Insurer), fmt.Sprintf("%d", client.ID))
// 	respondJSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("Proposal request for '%s' sent successfully to %s.", client.Name, product.Insurer)})
// }

// --- Middleware ---
func setupCORS(allowedOrigin string) func(next http.Handler) http.Handler {
	return cors.Handler(cors.Options{AllowedOrigins: []string{allowedOrigin}, AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}, AllowedHeaders: []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"}, ExposedHeaders: []string{"Link"}, AllowCredentials: true, MaxAge: 300})
}
func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			respondError(w, http.StatusUnauthorized, "Authorization header required")
			return
		}
		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			respondError(w, http.StatusUnauthorized, "Authorization header format must be Bearer {token}")
			return
		}
		tokenString := parts[1]
		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return jwtSecretKey, nil
		})
		if err != nil {
			if errors.Is(err, jwt.ErrTokenExpired) {
				log.Printf("AUTH: Expired token used.")
				respondError(w, http.StatusUnauthorized, "Token has expired")
			} else if errors.Is(err, jwt.ErrTokenSignatureInvalid) {
				respondError(w, http.StatusUnauthorized, "Invalid token signature")
			} else {
				log.Printf("AUTH: Invalid token error: %v", err)
				respondError(w, http.StatusUnauthorized, "Invalid token")
			}
			return
		}
		if !token.Valid || claims == nil {
			respondError(w, http.StatusUnauthorized, "Invalid token")
			return
		}
		log.Printf("AUTH: Valid token received for UserID: %d, Type: %s", claims.UserID, claims.UserType)
		ctx := context.WithValue(r.Context(), userIDKey, claims.UserID)
		ctx = context.WithValue(ctx, userTypeKey, claims.UserType)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func agencyOnlyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userType, ok := getUserTypeFromContext(r.Context())
		if !ok || userType != "agency" {
			log.Printf("AUTH: Forbidden access attempt by non-agency user type '%s'", userType)
			respondError(w, http.StatusForbidden, "Forbidden: Agency access required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- Main Function ---
func main() {
	// Load Configuration
	jwtSecretEnv := os.Getenv("JWT_SECRET")
	if jwtSecretEnv == "" {
		log.Println("WARNING: JWT_SECRET...")
		jwtSecretEnv = "DEFAULT_INSECURE_SECRET_CHANGE_ME_REALLY_CHANGE_ME"
	}
	frontendURLEnv := os.Getenv("FRONTEND_URL")
	if frontendURLEnv == "" {
		frontendURLEnv = "http://localhost:3000"
	} // Default frontend URL
	backendURLEnv := os.Getenv("BACKEND_URL")
	if backendURLEnv == "" {
		backendURLEnv = "http://localhost:8080"
	} // Default frontend URL
	dbDSN := os.Getenv("DB_DSN")

	// Fallback to manual construction if DB_DSN is not set
	if dbDSN == "" {
		dbUser := os.Getenv("DB_USERNAME")
		dbHost := os.Getenv("DB_HOST")
		dbPassword := os.Getenv("DB_PASSWORD")
		dbName := os.Getenv("DBNAME")
		// if dbUser == "" || dbHost == "" || dbPassword == "" || dbName == "" {
		// 	dbUser = "root"
		// 	dbHost = "127.0.0.1:3306"
		// 	dbPassword = "admin"
		// 	dbName = "admin"
		// }
		print("DB Username: ", dbUser)
		print("DB Host: ", dbHost)
		print("DB Password: ", dbPassword)
		print("DB Name: ", dbName)
		dbDSN = dbUser + ":" + dbPassword + "@unix(" + dbHost + ")/" + dbName + "?parseTime=true"
		// dbDSN = "root:admin@tcp(localhost:3306)/admin?parseTime=true"
		log.Println("WARNING: DB_DSN environment variable not set, using constructed DSN. THIS IS NOT FOR PRODUCTION.")
	}

	expiryHoursStr := os.Getenv("JWT_EXPIRY_HOURS")
	expiryHours, err := strconv.Atoi(expiryHoursStr)
	if err != nil || expiryHours <= 0 {
		expiryHours = 24
	}
	uploadPathEnv := os.Getenv("UPLOAD_PATH")
	if uploadPathEnv == "" {
		uploadPathEnv = "./uploads"
	}

	config = Config{ListenAddr: ":8080", DBDSN: dbDSN, VerificationURL: backendURLEnv + "/verify?token=", ResetURL: backendURLEnv + "/reset-password?token=", MockEmailFrom: "clientwise.co@gmail.com", CorsOrigin: "*", JWTSecret: jwtSecretEnv, JWTExpiryHours: expiryHours, UploadPath: uploadPathEnv, FrontendURL: frontendURLEnv}
	jwtSecretKey = []byte(config.JWTSecret)

	// Initialize Database
	if err := setupDatabase(); err != nil {
		log.Fatalf("FATAL: Database setup failed: %v", err)
	}

	// Setup Chi Router
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(setupCORS(config.CorsOrigin))

	// Public auth routes
	r.Post("/signup", handleSignup)
	r.Get("/verify", handleVerifyEmail)
	r.Post("/login", handleLogin)
	r.Post("/forgot-password", handleForgotPassword)
	r.Post("/reset-password", handleResetPassword)
	r.Post("/api/onboard", handlePublicOnboarding)
	r.Get("/api/unique-insurers", handleGetUniqueInsurers)
	r.Route("/api/portal/client/{token}", func(r chi.Router) {
		r.Get("/", handleGetPublicClientData)
		r.Post("/documents", handlePublicDocumentUpload)
	})

	// Protected API routes group
	r.Group(func(r chi.Router) {
		r.Use(authMiddleware) // Apply JWT auth

		r.Get("/api/notices", handleGetNotices)

		r.Get("/api/product-list", productsHandler) // Register new handler

		r.Get("/api/clients-info", handleGetClients)

		// Product routes
		r.Get("/api/products", handleGetProducts)
		r.Get("/api/products/{productId}", handleGetProduct)
		r.With(agencyOnlyMiddleware).Post("/api/products", handleCreateProduct) // Add Product (Agency Only)

		r.Route("/api/agents", func(r chi.Router) {
			r.Get("/profile", handleGetAgentProfile)
			r.Put("/profile", handleUpdateAgentProfile)
			r.Get("/goals", handleGetAgentGoal)
			r.Put("/goals", handleUpdateAgentGoal)
			r.Get("/my-clients-full-data", handleGetAgentFullClientData)
			r.Post("/suggest-tasks", handleSuggestAgentTasks)
			r.Get("/sales-performance", handleGetSalesPerformance)
			r.Put("/insurer-pocs", handleUpdateAgentInsurerPOCs)
			r.Put("/insurer-relations", handleUpdateAgentInsurerRelations)

		})

		// Client routes
		r.Get("/api/clients", handleGetClients)
		r.Post("/api/clients", handleCreateClient)
		r.Post("/api/clients/bulk-upload", handleBulkClientUpload) // NEW: Bulk upload

		r.Route("/api/clients/{clientId}", func(r chi.Router) {

			r.Get("/", handleGetClient)
			r.Put("/", handleUpdateClient)

			// r.Delete("/", handleDeleteClient) // Excluded

			// Nested routes for related data
			r.Get("/policies", handleGetClientPolicies)
			r.Post("/policies", handleCreateClientPolicy)
			r.Get("/communications", handleGetClientCommunications)
			r.Post("/communications", handleCreateClientCommunication)
			r.Get("/tasks", handleGetClientTasks)
			r.Post("/tasks", handleCreateClientTask)
			r.Get("/documents", handleGetClientDocuments)
			r.Post("/documents", handleUploadClientDocument)
			r.Get("/coverage-estimation", handleGetCoverageEstimation)
			r.Post("/generate-portal-link", handleGeneratePortalLink)
			r.Post("/suggest-tasks", handleSuggestClientTasks)
			r.Put("/insurer-details", handleUpdateAgentInsurerDetails)

		})

		// Proposal Route
		r.Route("/api/proposals", func(r chi.Router) {
			r.Post("/send", handleSendProposalEmail) // Uses updated logic
		})

		// Marketing Routes
		r.Route("/api/marketing", func(r chi.Router) {
			r.Get("/campaigns", handleGetMarketingCampaigns)
			r.Post("/campaigns", handleCreateMarketingCampaign) // Added
			r.Get("/templates", handleGetMarketingTemplates)
			r.Get("/content", handleGetMarketingContent)
			r.Get("/segments", handleGetClientSegments)
			r.Post("/segments", handleCreateClientSegment)
			//    r.Route("/segments", func(r chi.Router) {
			//  r.Get("/", handleGetClientSegments)      // GET /api/marketing/segments
			r.Post("/", handleCreateClientSegment)           // POST /api/marketing/segments
			r.Get("/{segmentId}", handleGetClientSegment)    // NEW: GET /api/marketing/segments/{id}
			r.Put("/{segmentId}", handleUpdateClientSegment) // N
		})

		// --- NEW: Dashboard Routes ---
		r.Route("/api/dashboard", func(r chi.Router) {
			r.Get("/metrics", handleGetDashboardMetrics)
			r.Get("/tasks", handleGetDashboardTasks)
			r.Get("/activity", handleGetDashboardActivity)

		})
		// r.Get("/api/tasks", handleGetAllTasks)        // Get all tasks for agent (paginated)
		r.Route("/api/policies", func(r chi.Router) { // Group policy related routes
			r.Get("/renewals", handleGetRenewals) // Get upcoming renewals
			// Add other policy-level routes here if needed
		})

		r.Get("/api/commissions", handleGetCommissions)
		r.Get("/api/tasks", handleGetAllTasks)            // Get all tasks for agent (paginated)
		r.Put("/api/task/status", handleUpdateTaskStatus) // Update task status
		r.Get("/api/activity", handleGetFullActivityLog)

	})

	// Start Server

	log.Printf("SERVER: Starting server on %s, allowing requests from %s using Chi router\n", config.ListenAddr, config.CorsOrigin)
	err = http.ListenAndServe(config.ListenAddr, r)
	if err != nil {
		log.Fatalf("FATAL: Could not start server: %v", err)
	}

}
