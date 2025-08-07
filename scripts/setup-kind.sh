#!/bin/bash
# setup-kind.sh - Kind cluster setup script for go-judgy

set -e

CLUSTER_NAME="go-judgy-local"
KIND_CONFIG="deployments/kind/cluster.yaml"
REGISTRY_NAME="kind-registry"
REGISTRY_PORT="5001"
CLUSTER_TIMEOUT=300  # 5 minutes default

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if kind is installed
check_kind() {
    if ! command -v kind &> /dev/null; then
        log_error "kind is not installed. Please install kind first:"
        log_info "https://kind.sigs.k8s.io/docs/user/quick-start/#installation"
        exit 1
    fi
    log_info "kind version: $(kind version)"
}

# Check if docker is running and has sufficient resources
check_docker() {
    if ! docker info &> /dev/null; then
        log_error "Docker is not running. Please start Docker first."
        exit 1
    fi

    # Check Docker resources
    local docker_info=$(docker info 2>/dev/null)
    local total_memory=$(echo "$docker_info" | grep "Total Memory" | awk '{print $3$4}')

    log_info "Docker is running (Memory: ${total_memory:-"unknown"})"

    # Check for common Docker issues that cause Kind to hang
    if docker ps &> /dev/null; then
        log_info "Docker daemon is responsive"
    else
        log_warning "Docker daemon seems slow to respond - this may cause cluster creation to hang"
        log_info "Consider running: docker system prune"
    fi
}

# Check if kubectl is installed
check_kubectl() {
    if ! command -v kubectl &> /dev/null; then
        log_warning "kubectl is not installed. Please install kubectl for cluster interaction:"
        log_info "https://kubernetes.io/docs/tasks/tools/install-kubectl/"
    else
        log_info "kubectl version: $(kubectl version --client --short 2>/dev/null || echo 'unknown')"
    fi
}

# Create local Docker registry for kind
create_registry() {
    log_info "Setting up local Docker registry..."

    # Check if registry already exists
    if docker ps -a | grep -q ${REGISTRY_NAME}; then
        if docker ps | grep -q ${REGISTRY_NAME}; then
            log_info "Registry ${REGISTRY_NAME} is already running"
            return
        else
            log_info "Starting existing registry ${REGISTRY_NAME}"
            docker start ${REGISTRY_NAME}
            return
        fi
    fi

    # Create new registry
    docker run -d \
        --restart=unless-stopped \
        --name ${REGISTRY_NAME} \
        -p "127.0.0.1:${REGISTRY_PORT}:5000" \
        registry:2

    log_success "Local Docker registry created at localhost:${REGISTRY_PORT}"
}

# Create kind cluster with timeout handling
create_cluster() {
    log_info "Creating kind cluster: ${CLUSTER_NAME} (timeout: ${CLUSTER_TIMEOUT}s)"

    # Check if cluster already exists
    if kind get clusters | grep -q "^${CLUSTER_NAME}$"; then
        log_warning "Cluster ${CLUSTER_NAME} already exists"
        read -p "Do you want to delete and recreate it? (y/N): " -n 1 -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            log_info "Deleting existing cluster..."
            kind delete cluster --name ${CLUSTER_NAME}
        else
            log_info "Using existing cluster"
            return
        fi
    fi

    # Create cluster with configuration
    log_info "This may take several minutes - please wait..."

    # Use timeout to prevent hanging
    if timeout ${CLUSTER_TIMEOUT} kind create cluster --name ${CLUSTER_NAME} --config ${KIND_CONFIG}; then
        log_success "Kind cluster ${CLUSTER_NAME} created successfully"
    else
        log_error "Cluster creation timed out or failed after ${CLUSTER_TIMEOUT} seconds"
        log_info "Try running with CLUSTER_TIMEOUT=600 for slower systems"
        log_info "Or check Docker resources and try again"
        exit 1
    fi

    # Validate cluster is actually ready
    log_info "Validating cluster readiness..."
    if ! kubectl cluster-info --context kind-${CLUSTER_NAME} &> /dev/null; then
        log_warning "Cluster created but not immediately ready - this is normal"
    fi
}

# Connect registry to kind network
connect_registry() {
    log_info "Connecting registry to kind network..."

    # Connect registry to kind network if not already connected
    if ! docker network inspect kind | grep -q ${REGISTRY_NAME}; then
        docker network connect kind ${REGISTRY_NAME}
        log_success "Registry connected to kind network"
    else
        log_info "Registry already connected to kind network"
    fi
}

# Configure kubectl context
configure_kubectl() {
    log_info "Configuring kubectl context..."

    if command -v kubectl &> /dev/null; then
        kubectl cluster-info --context kind-${CLUSTER_NAME}
        log_success "kubectl configured for cluster: kind-${CLUSTER_NAME}"
    else
        log_warning "kubectl not available - skipping context configuration"
    fi
}

# Check if kind cluster configuration exists
check_kind_config() {
    if [ ! -f "${KIND_CONFIG}" ]; then
        log_error "Kind configuration not found: ${KIND_CONFIG}"
        log_info "Please ensure the Kind cluster configuration file exists."
        log_info "Expected location: ${KIND_CONFIG}"
        exit 1
    fi
    log_info "Using kind configuration: ${KIND_CONFIG}"
}

# Main execution
main() {
    log_info "Starting kind cluster setup for go-judgy..."

    # Pre-flight checks
    check_kind
    check_docker
    check_kubectl

    # Setup steps
    check_kind_config
    create_registry
    create_cluster
    connect_registry
    configure_kubectl

    log_success "Kind cluster setup complete!"
    echo
    log_info "Cluster: ${CLUSTER_NAME}"
    log_info "Registry: localhost:${REGISTRY_PORT}"
    log_info "Context: kind-${CLUSTER_NAME}"
    echo
    log_info "Next steps:"
    log_info "1. Deploy the application: make deploy"
    log_info "2. Check cluster status: kubectl get nodes"
    log_info "3. View running pods: kubectl get pods -A"
}

# Script entry point
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi
