package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

type Coordinate struct {
	ID string  `json:"id"`
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
	Z  float64 `json:"z"`
}

type TargetChallenge struct {
	ChallengeID string       `json:"challenge_id"`
	PlayerPos   Coordinate   `json:"player_position"`
	Targets     []Coordinate `json:"targets"`
	Timestamp   int64        `json:"timestamp"`
}

type TargetResponse struct {
	ChallengeID string `json:"challenge_id"`
	ClosestID   string `json:"closest_target_id"`
}

type ChallengeData struct {
	Challenge      TargetChallenge
	ExpectedAnswer string
	CreatedAt      time.Time
}

var (
	activeChallenges = make(map[string]*ChallengeData)
	challengeMutex   = sync.RWMutex{}
)

// Calculate 3D distance between two points
func distance3D(p1, p2 Coordinate) float64 {
	dx := p1.X - p2.X
	dy := p1.Y - p2.Y
	dz := p1.Z - p2.Z
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// Find the closest target to player position
func findClosestTarget(playerPos Coordinate, targets []Coordinate) string {
	if len(targets) == 0 {
		return ""
	}
	
	closestID := targets[0].ID
	minDistance := distance3D(playerPos, targets[0])
	
	for _, target := range targets[1:] {
		dist := distance3D(playerPos, target)
		if dist < minDistance {
			minDistance = dist
			closestID = target.ID
		}
	}
	
	return closestID
}

// Generate random target challenge
func generateChallenge() TargetChallenge {
	challengeID := fmt.Sprintf("TARG-%d", rand.Intn(999999))
	
	// Player is always at origin
	playerPos := Coordinate{ID: "PLAYER", X: 0, Y: 0, Z: 0}
	
	// Generate 5-8 random targets
	numTargets := 5 + rand.Intn(4) // 5-8 targets
	targets := make([]Coordinate, numTargets)
	
	for i := 0; i < numTargets; i++ {
		targets[i] = Coordinate{
			ID: fmt.Sprintf("T%d", i+1),
			X:  (rand.Float64() - 0.5) * 200, // -100 to 100
			Y:  (rand.Float64() - 0.5) * 200, // -100 to 100
			Z:  (rand.Float64() - 0.5) * 100, // -50 to 50 (less variation in Z)
		}
	}
	
	return TargetChallenge{
		ChallengeID: challengeID,
		PlayerPos:   playerPos,
		Targets:     targets,
		Timestamp:   time.Now().Unix(),
	}
}

// Clean up expired challenges (older than 2 seconds)
func cleanupExpiredChallenges() {
	challengeMutex.Lock()
	defer challengeMutex.Unlock()
	
	cutoff := time.Now().Add(-2 * time.Second)
	for id, data := range activeChallenges {
		if data.CreatedAt.Before(cutoff) {
			delete(activeChallenges, id)
		}
	}
}

// GET /mission/coordinates - Serve challenge
func coordinatesChallengeHandler(w http.ResponseWriter, r *http.Request) {
	
	challenge := generateChallenge()
	
	// Calculate expected answer
	expectedAnswer := findClosestTarget(challenge.PlayerPos, challenge.Targets)
	
	// Store challenge data
	challengeMutex.Lock()
	activeChallenges[challenge.ChallengeID] = &ChallengeData{
		Challenge:      challenge,
		ExpectedAnswer: expectedAnswer,
		CreatedAt:      time.Now(),
	}
	challengeMutex.Unlock()
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(challenge)
	
	// Log with distances for debugging
	log.Printf("Challenge issued: %s", challenge.ChallengeID)
	for _, target := range challenge.Targets {
		dist := distance3D(challenge.PlayerPos, target)
		marker := ""
		if target.ID == expectedAnswer {
			marker = " ⭐ CLOSEST"
		}
		log.Printf("  %s: (%.2f, %.2f, %.2f) - Distance: %.2f%s", 
			target.ID, target.X, target.Y, target.Z, dist, marker)
	}
}

// POST /mission/coordinates - Submit answer
func coordinatesAnswerHandler(w http.ResponseWriter, r *http.Request) {
	
	var response TargetResponse
	if err := json.NewDecoder(r.Body).Decode(&response); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	
	challengeMutex.RLock()
	challengeData, exists := activeChallenges[response.ChallengeID]
	challengeMutex.RUnlock()
	
	if !exists {
		http.Error(w, "Challenge not found or expired", http.StatusNotFound)
		return
	}
	
	// Check if within time limit (1 second)
	elapsed := time.Since(challengeData.CreatedAt)
	if elapsed > time.Second {
		http.Error(w, "TIME EXPIRED! Target lost, soldier!", http.StatusRequestTimeout)
		log.Printf("TIMEOUT: %s took %.2f seconds", response.ChallengeID, elapsed.Seconds())
		return
	}
	
	// Check if answer is correct
	if response.ClosestID == challengeData.ExpectedAnswer {
		w.Header().Set("Content-Type", "application/json")
		result := map[string]interface{}{
			"status":       "TARGET ACQUIRED",
			"message":      "Closest target identified! Excellent work.",
			"response_time": fmt.Sprintf("%.3f seconds", elapsed.Seconds()),
			"challenge_id": response.ChallengeID,
			"target_id":    response.ClosestID,
		}
		json.NewEncoder(w).Encode(result)
		log.Printf("SUCCESS: %s identified %s in %.3f seconds", 
			response.ChallengeID, response.ClosestID, elapsed.Seconds())
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		
		// Find the actual distances for feedback
		var correctDistance, chosenDistance float64
		for _, target := range challengeData.Challenge.Targets {
			dist := distance3D(challengeData.Challenge.PlayerPos, target)
			if target.ID == challengeData.ExpectedAnswer {
				correctDistance = dist
			}
			if target.ID == response.ClosestID {
				chosenDistance = dist
			}
		}
		
		result := map[string]interface{}{
			"status":           "TARGET MISSED",
			"message":          "Wrong target! Check your distance calculations.",
			"correct_target":   challengeData.ExpectedAnswer,
			"chosen_target":    response.ClosestID,
			"correct_distance": fmt.Sprintf("%.2f", correctDistance),
			"chosen_distance":  fmt.Sprintf("%.2f", chosenDistance),
			"challenge_id":     response.ChallengeID,
		}
		json.NewEncoder(w).Encode(result)
		log.Printf("FAILED: %s chose %s (dist: %.2f) instead of %s (dist: %.2f)", 
			response.ChallengeID, response.ClosestID, chosenDistance, 
			challengeData.ExpectedAnswer, correctDistance)
	}
	
	// Remove completed challenge
	challengeMutex.Lock()
	delete(activeChallenges, response.ChallengeID)
	challengeMutex.Unlock()
}

// Status endpoint to show active challenges
func statusHandler(w http.ResponseWriter, r *http.Request) {
	challengeMutex.RLock()
	count := len(activeChallenges)
	challengeMutex.RUnlock()
	
	w.Header().Set("Content-Type", "application/json")
	status := map[string]interface{}{
		"server_status":      "OPERATIONAL",
		"active_challenges":  count,
		"challenge_timeout":  "1 second",
		"cleanup_interval":   "2 seconds",
		"player_position":    "(0, 0, 0)",
	}
	json.NewEncoder(w).Encode(status)
}

func main() {
	rand.Seed(time.Now().UnixNano())
	
	// Start cleanup routine
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			cleanupExpiredChallenges()
		}
	}()
	
	mux := http.NewServeMux()
	
	// Method-specific routing with new ServeMux API
	mux.HandleFunc("GET /mission/coordinates", coordinatesChallengeHandler)
	mux.HandleFunc("POST /mission/coordinates", coordinatesAnswerHandler)
	mux.HandleFunc("GET /status", statusHandler)
	
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, `TACTICAL TARGET ACQUISITION CHALLENGE SERVER
==========================================

Mission: Identify the closest target to your position (0,0,0)

Endpoints:
  GET  /mission/coordinates  - Get target identification challenge
  POST /mission/coordinates  - Submit closest target ID (< 1 second!)
  GET  /status              - Server status

Challenge Format:
  GET returns: {
    "challenge_id": "TARG-123456",
    "player_position": {"id": "PLAYER", "x": 0, "y": 0, "z": 0},
    "targets": [
      {"id": "T1", "x": 45.2, "y": -23.1, "z": 12.5},
      {"id": "T2", "x": -12.7, "y": 8.9, "z": -5.3},
      ...
    ],
    "timestamp": 1234567890
  }
  
  POST expects: {
    "challenge_id": "TARG-123456",
    "closest_target_id": "T1"
  }

Algorithm: Calculate 3D distance using √((x₁-x₂)² + (y₁-y₂)² + (z₁-z₂)²)
Time limit: 1 second from challenge issue to response!
`)
	})
	
	log.Printf("🎯 Tactical Target Acquisition Server starting on 0.0.0.0:6969")
	log.Printf("📍 Challenge endpoint: http://0.0.0.0:6969/mission/coordinates")
	log.Printf("⏱️  Time limit: 1 second per challenge")
	log.Printf("🎮 Player position: (0, 0, 0)")
	
	if err := http.ListenAndServe("0.0.0.0:6969", mux); err != nil {
		log.Fatal("Server failed to start:", err)
	}
}
